# grokslut

Personal Grok conversation export in Go: a reusable core, a CLI, a loopback web UI, and a small Chrome session bridge.

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

## Packages

- `auth`: session envelope and cookie jar
- `grok`: paginated list and response loading client
- `exporter`: Markdown, JSON, ZIP, and media writers
- `store`: shared private session persistence
- `mobile`: compact JSON façade for a future native client

## Verify

```sh
go test ./...
go vet ./...
```
