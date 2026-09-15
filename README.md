# grokslut

Personal Grok conversation export in Go: a reusable core, a CLI, a loopback web UI, a small Chrome session bridge, and a thin Kotlin Android client.

The exporter lists conversations, preserves project grouping and response branches, downloads media, and writes Markdown, JSON, or ZIP. It does not send messages or use the unrelated xAI model API.

## Browser workflow

1. Start the local exporter:

   ```sh
   go run ./cmd/grokslut-web
   ```

2. Open `chrome://extensions`, enable **Developer mode**, choose **Load unpacked**, and select the repository's `chrome-extension` directory.
3. Open `https://grok.com`, sign in normally, and let the conversation list load once.
4. Open the **grokslut session bridge** extension and click **Sync Grok session**.
5. Open `http://127.0.0.1:8787`. Conversations appear in a project tree; select chats and choose Markdown + media, JSON, or ZIP.

The extension reads cookies only for `grok.com`, observes only requests under `/rest/app-chat/`, and sends the resulting session envelope only to the loopback exporter. Challenge headers are kept in Chrome's in-memory session storage and cleared after a successful sync. No credential is logged or sent to another host.

Grok's history endpoints are private implementation details and may change without notice. This tool is intended only for exporting your own data.

## CLI

The browser bridge and CLI share the same session file in the user's configuration directory, so no additional login step is needed:

```sh
go run ./cmd/grokslut verify
go run ./cmd/grokslut list --all
go run ./cmd/grokslut export --ids chat-id-1,chat-id-2 --format zip --out exports
```

Pass `--session path/to/session.json` to either program only when an explicit alternate store is needed.

## Android

The Android client is feasible on an unrooted device, with an important limit: Grok has no third-party OAuth flow for chat history. A Custom Tab can share the browser's login but cannot give its cookies to the app. The client therefore signs in inside an app-owned WebView, captures only that WebView's Grok session, validates it through the Go core, and encrypts it with Android Keystore.

Google's OAuth policy rejects embedded user-agents, so **Continue with Google is not promised to work**. Direct Grok/X sign-in can work when Grok permits it. A session JSON produced by the desktop bridge can also be imported through Android's document picker; root access is not required.

Build requirements are Go 1.22+, JDK 17, an Android SDK/NDK, and Gradle 8.11.1+:

```sh
scripts/build-android.sh
```

The script binds `./mobile` into an AAR and then builds the Kotlin/Compose APK. See [android/README.md](android/README.md) for the security boundary and device test plan.

## Packages

- `auth`: session envelope and cookie jar
- `grok`: paginated list and response loading client
- `exporter`: Markdown, JSON, ZIP, and media writers
- `store`: shared private session persistence
- `mobile`: compact, gomobile-bindable JSON façade used by Android
- `android/app`: Kotlin/Compose UI, WebView session acquisition, Keystore storage, and SAF export

## Verify

```sh
go test ./...
go vet ./...
```
