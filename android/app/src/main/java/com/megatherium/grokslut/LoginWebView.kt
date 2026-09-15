package com.megatherium.grokslut

import android.annotation.SuppressLint
import android.content.Intent
import android.webkit.CookieManager
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.weight
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.ConcurrentHashMap

@SuppressLint("SetJavaScriptEnabled")
@Composable
internal fun LoginWebView(onCancel: () -> Unit, onSession: (String) -> Unit) {
    val context = LocalContext.current
    val capturedHeaders = remember { ConcurrentHashMap<String, String>() }
    val webView = remember {
        WebView(context).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.allowFileAccess = false
            settings.allowContentAccess = false
            val currentWebView = this
            CookieManager.getInstance().apply {
                setAcceptCookie(true)
                setAcceptThirdPartyCookies(currentWebView, true)
            }
            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                    val uri = request.url
                    if (uri.scheme == "https") return false
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, uri)) }
                    return true
                }

                override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest) =
                    super.shouldInterceptRequest(view, request).also {
                        if (request.url.host == "grok.com" && request.url.path.orEmpty().startsWith("/rest/app-chat/")) {
                            request.requestHeaders.forEach { (name, value) ->
                                if (keepHeader(name)) capturedHeaders[name.lowercase()] = value
                            }
                        }
                    }
            }
            loadUrl(GROK_URL)
        }
    }

    DisposableEffect(webView) { onDispose { webView.destroy() } }

    Column(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxWidth().padding(8.dp)) {
            TextButton(onClick = onCancel) { Text("Back") }
            Button(
                onClick = {
                    val cookies = CookieManager.getInstance().getCookie(GROK_URL).orEmpty()
                    onSession(sessionJson(cookies, capturedHeaders))
                },
                modifier = Modifier.weight(1f),
            ) { Text("Use this Grok session") }
        }
        Text(
            "Sign in, wait until the conversation list appears, then use this session. Google may refuse embedded sign-in; use Grok/X or import a desktop session instead.",
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp),
        )
        AndroidView(factory = { webView }, modifier = Modifier.fillMaxSize())
    }
}

private fun keepHeader(name: String): Boolean {
    val lower = name.lowercase()
    return lower.startsWith("x-") || lower == "user-agent" || lower == "accept-language"
}

private fun sessionJson(cookieHeader: String, headers: Map<String, String>): String {
    val cookies = JSONArray()
    cookieHeader.split(';').forEach { part ->
        val separator = part.indexOf('=')
        if (separator > 0) {
            cookies.put(
                JSONObject()
                    .put("name", part.substring(0, separator).trim())
                    .put("value", part.substring(separator + 1).trim())
                    .put("domain", ".grok.com"),
            )
        }
    }
    return JSONObject().put("cookies", cookies).put("headers", JSONObject(headers)).toString()
}

private const val GROK_URL = "https://grok.com/"
