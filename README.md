# grokslut

Android app (Go core) to sign into your Grok account, list chat conversations, and export the ones you pick.

**Status:** architecture brief only — implementation blocked until Cursor Cloud Agents are available on the account.

## Product snapshot

- Login via Grok’s **Continue with Google / X / Grok** flow (in-app WebView; session captured after redirect)
- List and multi-select chat threads
- Per-export format: Markdown + media, JSON, or a zip with both
- Core logic in Go; thin Kotlin/Compose UI

## Docs

See [ARCHITECTURE.md](./ARCHITECTURE.md) for layout, auth, unofficial grok.com REST notes, FFI, and MVP milestones.

## Licence

See [LICENSE](./LICENSE).
