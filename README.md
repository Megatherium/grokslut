# grokslut

Personal Grok and Gemini conversation export in Go: reusable provider clients, a CLI, a loopback web UI, a small Chrome session bridge, and a thin Kotlin Android client for Grok.

The exporter lists conversations, exhausts each provider's progressive-history pagination, preserves raw response data, downloads media, and writes Markdown, normalized JSON, raw archival JSON, or ZIP. It does not send messages or use the unrelated xAI or Google model APIs.

## Browser workflow

1. Start the local exporter:

   ```sh
   go run ./cmd/grokslut-web
   ```

2. Open `chrome://extensions`, enable **Developer mode**, choose **Load unpacked**, and select the repository's `chrome-extension` directory.
3. Open `https://grok.com` and/or `https://gemini.google.com`, sign in normally, and let each conversation list load once.
4. Open the **grokslut session bridge** extension and sync each provider you want to export.
5. Open `http://127.0.0.1:8787`. Choose Grok or Gemini, filter and select chats, then choose Markdown + media, JSON, Raw JSON, or ZIP. Large exports show live message-loading progress. JSON uses a compact provider-neutral schema; Raw JSON retains the providers' undocumented payloads for archival/debugging.

The exporter does not depend on either site's progressively loaded page UI. For Grok it hydrates the complete response index and follows unresolved parent links. For Gemini it walks every conversation-list and turn cursor, normalizes both human and assistant text, and retains the raw positional RPC records in Raw JSON.

The extension reads only cookies applicable to the selected first-party site, observes only Grok history or Gemini batched-RPC requests, and sends the resulting session envelope only to the loopback exporter. Transient headers are kept in Chrome's in-memory session storage and cleared after a successful sync. Grok and Gemini sessions are stored separately with owner-only permissions. No credential is logged or sent to another host.

Grok's history endpoints are private implementation details and may change without notice. This tool is intended only for exporting your own data.

## CLI

The browser bridge and CLI share the same session file in the user's configuration directory, so no additional login step is needed:

```sh
go run ./cmd/grokslut verify
go run ./cmd/grokslut list --all
go run ./cmd/grokslut export --ids chat-id-1,chat-id-2 --format zip --out exports

go run ./cmd/grokslut verify --provider gemini
go run ./cmd/grokslut list --provider gemini --all
go run ./cmd/grokslut export --provider gemini --ids c_example --format zip --out exports
go run ./cmd/grokslut export --provider gemini --ids c_example --format raw-json --out exports
```

Pass `--session path/to/session.json` only when an explicit alternate store is needed. Defaults are `session.json` for Grok and `session-gemini.json` for Gemini under the grokslut user configuration directory.

## Android

The Android client is feasible on an unrooted device, with an important limit: Grok has no third-party OAuth flow for chat history. A Custom Tab can share the browser's login but cannot give its cookies to the app. The client therefore signs in inside an app-owned WebView, captures only that WebView's Grok session, validates it through the Go core, and encrypts it with Android Keystore.

Google's OAuth policy rejects embedded user-agents, so **Continue with Google is not promised to work**. Direct Grok/X sign-in can work when Grok permits it. A session JSON produced by the desktop bridge can also be imported through Android's document picker; root access is not required.

Build requirements are Go 1.22+, JDK 17, and an Android SDK/NDK. The repository includes a pinned Gradle wrapper. With [mise](https://mise.jdx.dev/) installed, provision the toolchain and build with:

```sh
mise install
mise run android
```

Without mise, set `ANDROID_HOME` and `JAVA_HOME`, install Android platform 35, build tools 35.0.0 and NDK 27.2.12479018, then run `scripts/build-android.sh`.

The script binds `./mobile` into an AAR and then builds the Kotlin/Compose APK. See [android/README.md](android/README.md) for the security boundary and device test plan.

## Packages

- `auth`: session envelope and cookie jar
- `grok`: Grok list, response hydration, and ancestor recovery client
- `gemini`: Gemini batched-RPC list and cursor-complete turn hydration client
- `exporter`: Markdown, JSON, ZIP, and media writers
- `store`: shared private session persistence
- `mobile`: compact, gomobile-bindable JSON façade used by Android
- `android/app`: Kotlin/Compose UI, WebView session acquisition, Keystore storage, and SAF export

## Verify

```sh
go test ./...
go vet ./...
```
