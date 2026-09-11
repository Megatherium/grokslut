# grokslut — Architecture brief

**Goal:** Android app that signs into the user’s Grok account, lists chat conversations, and exports selected threads. Core logic in Go; thin Android UI.

**Repo:** `Megatherium/grokslut`  
**Status:** Brief only (Cloud Agents not on current plan). Ready to implement when they are.

---

## 1. Scope

### In
- Login via Grok’s normal **Continue with Google / X / Grok** flow (in-app WebView)
- List conversations (title, id, timestamps, optional preview)
- Multi-select conversations
- Per-export format picker:
  1. Markdown + media
  2. JSON (conversation + responses)
  3. Zip wrapping Markdown + JSON + media
- Secure on-device session storage
- Progress / error handling for long exports

### Out (MVP)
- Sending new messages / chatting
- Official xAI API key path (no public history API for this)
- Voice / audio archive
- Background sync or push

---

## 2. Hard constraint: unofficial chat history API

xAI does **not** publish an official “list/export my chats” API for grok.com. Community clients reverse-engineer cookie-authenticated REST under `https://grok.com/rest/app-chat/…`.

Observed (community-reported; verify at implement time — endpoints drift):

| Step | Method | Path |
|------|--------|------|
| List | `GET` | `/rest/app-chat/conversations?pageSize=60` |
| Node IDs | `GET` | `/rest/app-chat/conversations/{id}/response-node?includeThreads=true` |
| Bodies | `POST` | `/rest/app-chat/conversations/{id}/load-responses` body `{"responseIds":[…]}` |

Auth for those calls is the **browser session** established after Google / X / Grok login (cookies such as `sso` / `sso-rw`, plus challenge headers that may also appear: `x-anonuserid`, `x-challenge`, `x-signature`, sometimes `x-statsig-id`). Cloudflare / challenge churn is a known risk.

**Product implication:** login UX is first-party OAuth-style in a WebView; API access is “reuse that session,” not “paste cookies.” Document ToS / breakage risk in README.

**Fallback:** if REST list/load fails after login, surface a clear error; optional later: “Import `prod-grok-backend.json` from official data download” as a non-login path.

---

## 3. High-level architecture

```
┌─────────────────────────────────────────┐
│  Android (Kotlin + Jetpack Compose)     │
│  • LoginActivity / WebView              │
│  • Conversation list + multi-select     │
│  • Export format bottom sheet           │
│  • SAF / Downloads / share sheet        │
└──────────────────┬──────────────────────┘
                   │ JNI / gomobile API
┌──────────────────▼──────────────────────┐
│  Go core (`github.com/Megatherium/…`)   │
│  • auth.Session (cookie jar + headers)  │
│  • grok.Client (list / nodes / load)    │
│  • export.{Markdown,JSON,Zip}           │
│  • media downloader                     │
└─────────────────────────────────────────┘
                   │ HTTPS
              grok.com REST
```

Android owns UI + WebView cookie jar handoff. Go owns HTTP, pagination, export writers, path sanitisation.

---

## 4. Suggested repo layout

```
/
├── README.md
├── ARCHITECTURE.md          # this brief
├── go/
│   ├── go.mod
│   ├── auth/                # Session type, cookie jar, header bag
│   ├── grok/                # Client: ListConversations, LoadThread
│   ├── export/              # Markdown, JSON, Zip writers
│   ├── mobile/              # gomobile-bindable façade (stringly/JSON DTOs)
│   └── internal/testdata/   # recorded fixtures (redacted)
├── android/
│   ├── app/                 # Compose UI
│   ├── grokcore/            # AAR from gomobile bind
│   └── …
└── scripts/
    └── bind-android.sh      # gomobile bind → AAR
```

---

## 5. Auth: Continue with Google / X / Grok

### Flow
1. User taps **Sign in**.
2. App opens an in-app **Chrome Custom Tab or WebView** to `https://grok.com` (or the exact auth entry URL Grok uses at build time).
3. User completes **Continue with Google**, **X**, or **Grok** in that surface — same UX as the website.
4. On reaching an authenticated landing (e.g. `https://grok.com/` with chat UI, or a documented redirect), Android reads the **CookieManager** cookies for `.grok.com` / `grok.com`.
5. Android passes cookies (+ any required static client headers discovered during probe) into Go via `SessionFromCookies(json)`.
6. Go verifies with a cheap authenticated call (`GET …/conversations?pageSize=1`). Success → persist session; failure → stay on login with error.
7. **Sign out** clears CookieManager + encrypted session store.

### Storage
- EncryptedSharedPreferences or Android Keystore-backed store for the cookie blob.
- Never log full cookies. Never commit them.
- Treat session as short-lived; re-open WebView on 401 / challenge failure.

### Implementation notes
- Prefer **Custom Tabs** for Google/X OAuth trust, but cookie capture often needs a **WebView** with third-party cookies enabled for the grok.com domain after redirect — validate both; ship whichever actually yields a usable jar.
- Do **not** ask the user to paste DevTools cookies. That path is debug-only.
- Handle account chooser / 2FA inside the WebView; app does not implement IdP logic.

---

## 6. Go core API (façade for mobile)

Keep the bind surface small and JSON-friendly:

```text
SessionFromCookies(cookiesJSON string) error
SessionClear()
SessionIsValid() bool

ListConversations(pageSize int, cursor string) (listJSON string, nextCursor string, err error)
LoadConversation(id string) (threadJSON string, err error)

ExportConversations(idsJSON string, format string, outDir string) (resultJSON string, err error)
# format ∈ "markdown" | "json" | "zip"
```

Internal types (not necessarily exported across FFI):

- `ConversationSummary{ID, Title, UpdatedAt, CreatedAt, Preview}`
- `Thread{Conversation, Responses[]}` — preserve parent/child ids for branching
- `ExportResult{Paths[], Warnings[]}`

### Client behaviour
- Shared `http.Client` with cookie jar
- Pagination until user stops or cursor exhausted
- Batch `load-responses` (chunk responseIds; community scripts use ~50–100)
- Rate-limit politely; retry transient 429/5xx with backoff
- Map HTTP 401/403 → `ErrAuthExpired` for UI to re-login

### Media
- Collect `generatedImageUrls` / asset URLs from responses
- Download into `<export>/<sanitized-title>/media/`
- Rewrite Markdown links to relative paths
- Skip or warn on inaccessible assets (common for some Imagine URLs)

---

## 7. Export formats (chosen **per export action**)

UI: after selection, bottom sheet / dialog:

1. **Markdown + media** — one `.md` per chat + `media/`; YAML/front-matter optional (id, title, dates)
2. **JSON** — `{ "conversation": …, "responses": […] }` close to Grok’s own shape so other tools can ingest
3. **Zip** — both of the above + media in one archive per chat (or one zip with a folder per chat — prefer **one zip containing one folder per selected chat** for multi-select)

Default last-used format in preferences; still always show the picker.

Destination: Storage Access Framework tree URI or app-specific external dir + share intent.

---

## 8. Android UI (Compose)

| Screen | Behaviour |
|--------|-----------|
| Login | CTA → WebView/Custom Tab flow; error banner |
| Chats | Lazy list, pull-to-refresh, multi-select, select-all |
| Export sheet | Format radio + Confirm; progress dialog with cancel |
| Settings | Sign out, open export folder, “About / unofficial API” note |

Empty / loading / error states required. Offline: show cached list only if we add a local DB later; MVP can require network for list+export.

---

## 9. Build & FFI

- Go 1.22+; `gomobile bind -target=android -o android/grokcore/grokcore.aar ./mobile`
- Gradle module consumes AAR; CI script documents NDK / ANDROID_HOME
- Unit-test Go with httptest fixtures (redacted)
- Android UI tests optional for MVP; manual login on a real device recommended (emulator + Google login is painful)

---

## 10. MVP milestones

1. **Spike:** WebView Google/X/Grok login → extract cookies → `GET /conversations` succeeds on device
2. **List + load:** paginated list UI; open one thread in memory
3. **Export:** Markdown+media and JSON for one chat; then zip; then multi-select
4. **Harden:** auth expiry, progress, README, redacted fixtures

Do not ship UI stubs that pretend to talk to Grok — spike (1) gates the rest.

---

## 11. Risks (document in README)

- Unofficial endpoints and challenge headers change without notice
- Cloudflare / bot checks may break headless-style clients; real WebView session is the mitigation
- ToS: personal export of own data is the intended use; no redistribution of scraped third-party content
- Media URLs may expire or require the same cookies

---

## 12. When Cloud Agents are available

Implement in `Megatherium/grokslut` against this brief: spike auth first, then list/load/export, open a PR. This file is the source of truth until product decisions change.
