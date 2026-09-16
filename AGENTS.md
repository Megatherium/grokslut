# AGENTS.md

Guidance for AI agents working in this repository.

## What this is

`grokslut` exports a user's own Grok (grok.com) and Gemini (gemini.google.com) conversation history. It speaks the sites' **private, undocumented browser endpoints** — Grok `/rest/app-chat/*` and Gemini `/_/BardChatUi/data/batchexecute` — not either provider's model API. It never sends messages. The Go core is shared by a CLI, a loopback web UI, a Chrome MV3 session bridge, and a Kotlin/Compose Android client bound to the Grok core via gomobile.

Because the upstream API is unofficial and changes without notice, the codebase's central convention is **defensive JSON decoding**: nearly every field is read through fallback key lists (`"responseId"`, `"id"`, `"response_id"`; `"conversations"`, `"items"`, `"data"`, etc.) via `firstString`/`firstArray`/`responseString` helpers, and raw payloads are preserved (`Raw json.RawMessage`) alongside the parsed summary. Any new field parsing must follow this pattern rather than assuming one canonical shape.

## Commands

```sh
go test ./...        # test everything (the only test gate)
go vet ./...         # the only linter; no golangci-lint or other lint config exists

go run ./cmd/grokslut verify                       # check the stored session
go run ./cmd/grokslut list --all                  # list conversations (JSON to stdout)
go run ./cmd/grokslut export --ids ID,ID --format zip --out exports
go run ./cmd/grokslut list --provider gemini --all
go run ./cmd/grokslut export --provider gemini --ids c_ID --format zip --out exports

go run ./cmd/grokslut-web                         # web UI at http://127.0.0.1:8787

mise install          # provision Go 1.22, JDK 17, Android SDK (pinned in .mise.toml)
mise run android      # build Go AAR + debug APK (wraps scripts/build-android.sh)
```

Android build without mise: set `ANDROID_HOME` and `JAVA_HOME` (JDK 17), then `scripts/build-android.sh`. The script installs a **pinned gomobile** (`GOMOBILE_VERSION`, matching the `golang.org/x/mobile` version in `go.mod`), binds `./mobile` into `android/app/libs/grokcore.aar`, then runs `./gradlew :android:app:assembleDebug`. Android targets: platform/build-tools 35, NDK 27.2.12479018, minSdk 26.

There is no CI pipeline; `go test` + `go vet` is the whole gate. Commits follow conventional style (`feat:`, `fix:`).

## Architecture and data flow

Session acquisition is the crux — Grok has no third-party OAuth for chat history, so a *browser session envelope* is passed around:

```
Chrome extension (popup.js + background.js)
  ├─ captures grok.com cookies (must include `sso` or `sso-rw`) + challenge headers
     (x-anonuserid, x-challenge, x-signature, x-statsig-id, user-agent, accept-language)
     observed on /rest/app-chat/* requests; headers live in chrome.storage.session
     and are cleared after a successful sync
  └─ captures cookies applicable to gemini.google.com (must include `__Secure-1PSID`
     or `__Secure-3PSID`) + user-agent/language observed only on Gemini batched RPCs
  └─ POST {cookies, headers} → web UI /api/session/{grok|gemini}
       └─ store.Save: atomic 0600 provider files under os.UserConfigDir()/grokslut/
          (`session.json` and `session-gemini.json`)

CLI: reads the same session file (no separate login)
Android: signs in inside an app-owned WebView (CookieManager capture) or imports
         the same JSON envelope via the document picker; encrypts it with Android Keystore
```

Packages (dependency direction: everything → auth/grok):

- `auth` — the session envelope. `FromJSON` accepts either `{cookies, headers}` or a bare cookie array (FFI callers). `Apply` installs cookies into an `http.Client` jar and **rejects cookies whose domain doesn't match the base URL host**.
- `grok` — HTTP client. `NewClient` filters session headers through an allowlist (`x-*`, `user-agent`, `accept-language` only; `Authorization` is deliberately not forwardable) and **strips session headers on any cross-origin redirect**. 401/403 map to the sentinel `grok.ErrAuthExpired`; other failures use typed `*HTTPError`. Retries (default 3) on 429/5xx honoring `Retry-After` with exponential backoff. `LoadConversationProgress` hydrates the full response index (`/response-node?includeThreads=true`), batches `POST /load-responses` (75 per batch), then **loops following unresolved `parentResponseId` links** until the ancestry is complete, erroring if Grok never returns a referenced ancestor. This bypasses the web UI's scroll-based progressive loading — do not regress it.
- `gemini` — read-only Gemini batched-RPC client. It bootstraps the page's `SNlM0e`/`cfb2h`/`FdrFJe` values, uses `MaZiqc` for conversation pages (including the separate pinned set), and uses `hNvQHb` for 100-turn pages. **Always exhaust the opaque cursor and reject repeated cursors**; a single response page is not a complete conversation. Gemini records are positional arrays: decode known fields defensively, normalize human/assistant `message`, IDs, parent links, `createTime`, and attachments for the shared exporter, and retain raw turns/pages in JSON. Missing bootstrap tokens map to `grok.ErrAuthExpired` so CLI/web behavior stays consistent.
- `exporter` — formats `markdown`, `json`, `zip`. Markdown: YAML frontmatter, responses stably sorted by `createTime`, empty bodies skipped. JSON: raw conversation + responses, preserving upstream shapes. ZIP: per-conversation dir with `.md` + `.json` + `media/`. Media downloads dedupe by URL, cap at 64 MB, and rewrite URLs in the Markdown body to local `media/` paths. `SafeName` sanitizes titles for the filesystem (path-escape tests exist — keep them passing). Progress is reported via callbacks with phases `loading`/`responses`/`writing`.
- `store` — atomic (temp-file + rename) 0600 session persistence shared by CLI and web UI.
- `mobile` — gomobile bind façade. Constraints: **package-level functions only**, return **a single JSON string + error** (gomobile reliably binds at most one return value across the JNI boundary), keep global client state behind the `state` mutex. It has no Android imports; keep it that way.
- `cmd/grokslut` — flag-based CLI. `--provider grok|gemini` selects the client and provider-specific default session. JSON results go to stdout, progress to stderr, and **exit code 3 specifically for `ErrAuthExpired`** (scripts may rely on it). `--base-url` exists to point the client at a local test server.
- `cmd/grokslut-web` — loopback-only server (`--listen` is validated; non-loopback addresses are fatal). It keeps independent provider clients, exposes provider status/selection, serves one embedded page (`page.html` via `go:embed`), and streams NDJSON progress from `/api/export-stream`. Mutating endpoints require a trusted `Origin` (empty, `chrome-extension://`, or same host). `tree()` groups conversations into projects, with unfiled chats under a synthetic "unfiled" group.
- `android/app` — Kotlin/Compose UI over the generated `grocore.aar`. The AAR is **generated, not committed** (gitignored), so the Gradle dependency is conditional on `libs/grokcore.aar` existing; you cannot build the APK without running the gomobile bind step first.

## Testing

- Standard library only — no testify/ginkgo; plain `if got != want { t.Fatalf(...) }`.
- Tests live in external `_test` packages (`package grok_test`), so only exported API is exercised.
- Network behavior is tested with `httptest.NewServer` fixtures that fake the `/rest/app-chat/` endpoints; point the client at the fixture via `grok.NewClient(session, server.URL)`. The small `fixtureClient`/`writeJSON` helpers are duplicated per test file (not shared) — follow that pattern.
- Gemini fixtures fake both `/app` bootstrap data and batchexecute's XSSI-prefixed `wrb.fr` envelope. Tests must cover a non-empty continuation cursor followed by a terminal page and assert that both human and assistant text survive normalization.
- Always set `client.Retries = 0` in fixtures, or error-path tests will retry pointlessly.
- There are no Kotlin/Android unit tests; android/README.md defines a manual real-device test gate instead.

## Gotchas

- **Don't run `go mod tidy` carelessly**: gopls reports `golang.org/x/mobile`, `x/mod`, `x/sync`, `x/tools` as unused (the `mobile` package doesn't import them), but the `x/mobile` pin is deliberately coordinated with `GOMOBILE_VERSION` in `scripts/build-android.sh`. Removing module pins can desync the Android toolchain.
- **`page.html` is embedded at compile time** (`go:embed`). Edits require restarting `go run ./cmd/grokslut-web`; there is no live reload.
- Conversation trees are cached in memory per provider for the lifetime of `grokslut-web`. Session sync/logout invalidates that provider; the UI's Refresh action sends `refresh=1` to replace the cache.
- **Secrets discipline is a feature, not a detail**: session cookies and challenge headers must never be logged, printed, or sent anywhere but the loopback exporter. Existing guarantees to preserve: header allowlist + cross-origin stripping in `grok`, cookie domain validation in `auth`, origin checks and `MaxBytesReader` limits in the web UI, `redactURL` in media warnings, 0600/0700 file modes in `store`. Several tests assert these properties directly — treat a failing security test as a regression, not a flake.
- **Challenge headers are transient**: they are bound to browser state, captured in-memory by the extension, and cleared after sync. An expired/rejected session must surface as `ErrAuthExpired` (CLI exit 3 / web HTTP 401), never as a crash or a fake success.
- `exports/` and `session*.json` are gitignored on purpose. Never commit or create real session files in the repo. Provider cookies remain isolated in their own files.
- Grok's page-visible DOM is never parsed; the client goes straight to the JSON endpoints. Anything that starts scraping HTML is off-architecture.
- Go version is 1.22: `min`/`max` builtins are available (and gopls will suggest them); the codebase sticks to the standard library with almost zero runtime dependencies.
