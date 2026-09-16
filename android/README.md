# Android client

This is a thin Kotlin/Compose client over the existing Go core. It targets Android 8.0 (API 26) and later and needs no privileged permissions, accessibility service, local VPN, browser database access, or root.

## What is possible

An app owns the cookie jar of WebViews created by that app. After the user signs in to Grok inside the embedded WebView, Android's `CookieManager` can provide the cookies that would be sent to `grok.com`. The client also observes request headers for Grok's own `/rest/app-chat/` requests. It passes those values to the Go core and persists them only after an authenticated conversation-list request succeeds.

The app can alternatively import the same `{cookies, headers}` JSON envelope used by the CLI and desktop browser bridge. The document picker grants access only to the file the user selects.

## What is not possible

- A sideloaded app cannot read Chrome's cookie database on an unrooted device.
- A Chrome Custom Tab shares Chrome's session with the tab, not with the calling app. There is no cookie handoff API.
- Grok does not expose an OAuth authorization-code flow for exporting the signed-in user's conversation history.
- Google forbids OAuth authorization in an embedded user-agent. If Grok sends Continue with Google into the WebView, Google may return `disallowed_useragent`.

Consequently, direct WebView sign-in is provider-dependent. Grok/X login or session import are the practical paths. Even with valid cookies, Grok can change its private endpoints or bind challenge headers to browser state; the app reports that as a rejected/expired session instead of claiming login succeeded.

## Security boundary

- The manifest requests only internet access.
- No `addJavascriptInterface` or native message bridge is exposed to Grok pages.
- Only `grok.com` `/rest/app-chat/` request headers are observed, and only `x-*`, user-agent, and language headers are retained.
- Session JSON is encrypted with a non-exportable AES-GCM key in Android Keystore.
- Sign out deletes both ciphertext and the Keystore key, then clears the Go in-memory client.
- Exports use the Storage Access Framework. The app stages files in its cache and copies them only to the folder selected by the user.

## Build

The repository pins its command-line toolchain with mise and includes a Gradle wrapper:

```sh
mise install
mise run android
```

Without mise, install Go 1.22+, JDK 17, Android platform 35, build tools 35.0.0, and NDK 27.2.12479018. Set `ANDROID_HOME` and `JAVA_HOME`, then run `scripts/build-android.sh`.

The script installs the pinned `gomobile`, produces `android/app/libs/grokcore.aar`, and assembles a debug APK with `./gradlew`. The AAR is generated and intentionally not committed.

## Real-device gate

Before treating the app as releasable, verify on a physical device:

1. Direct Grok and X login, including account chooser and 2FA.
2. Google login's expected refusal and the session-import fallback.
3. Conversation pagination and project grouping against current response shapes.
4. Markdown, JSON, and ZIP export to removable and internal SAF locations.
5. Media downloads and challenge renewal after the WebView has been idle.
6. Sign-out followed by confirmation that the old session cannot be reused.
