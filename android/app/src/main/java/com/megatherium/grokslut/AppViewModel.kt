package com.megatherium.grokslut

import android.app.Application
import android.net.Uri
import android.webkit.CookieManager
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File

internal data class UiState(
    val coreAvailable: Boolean = true,
    val signedIn: Boolean = false,
    val busy: Boolean = true,
    val conversations: List<Conversation> = emptyList(),
    val selected: Set<String> = emptySet(),
    val error: String? = null,
    val notice: String? = null,
)

internal class AppViewModel(application: Application) : AndroidViewModel(application) {
    private val store = SessionStore(application)
    private val coreResult = GrokCore.load()
    private val core get() = coreResult.getOrThrow()
    private val mutableState = MutableStateFlow(UiState(coreAvailable = coreResult.isSuccess))
    val state: StateFlow<UiState> = mutableState.asStateFlow()

    init {
        val saved = store.load()
        if (saved == null || coreResult.isFailure) {
            mutableState.value = mutableState.value.copy(busy = false)
        } else {
            connect(saved, persist = false)
        }
    }

    fun connect(sessionJson: String, persist: Boolean = true) {
        runTask {
            require((JSONObject(sessionJson).optJSONArray("cookies")?.length() ?: 0) > 0) {
                "No grok.com cookies were found. Finish signing in first."
            }
            core.setSession(sessionJson)
            val conversations = loadAll()
            if (persist) store.save(sessionJson)
            mutableState.value = mutableState.value.copy(
                signedIn = true,
                conversations = conversations,
                selected = emptySet(),
                notice = "Connected to Grok.",
            )
        }
    }

    fun importSession(raw: String) = connect(raw)

    fun refresh() = runTask {
        mutableState.value = mutableState.value.copy(conversations = loadAll(), selected = emptySet())
    }

    fun toggle(id: String) {
        val selected = mutableState.value.selected.toMutableSet()
        if (!selected.add(id)) selected.remove(id)
        mutableState.value = mutableState.value.copy(selected = selected)
    }

    fun selectAll() {
        val state = mutableState.value
        val all = state.conversations.mapTo(mutableSetOf()) { it.id }
        mutableState.value = state.copy(selected = if (state.selected.size == all.size) emptySet() else all)
    }

    fun export(format: String, treeUri: Uri) = runTask {
        val ids = mutableState.value.selected
        require(ids.isNotEmpty()) { "Select at least one conversation." }
        val root = File(getApplication<Application>().cacheDir, "exports/${System.currentTimeMillis()}")
        try {
            root.mkdirs()
            core.exportConversations(idsJson(ids), format, root.absolutePath)
            copyExportToTree(getApplication(), root, treeUri)
            mutableState.value = mutableState.value.copy(notice = "Export finished.")
        } finally {
            root.deleteRecursively()
        }
    }

    fun signOut() {
        runCatching { if (coreResult.isSuccess) core.clearSession() }
        CookieManager.getInstance().removeAllCookies(null)
        CookieManager.getInstance().flush()
        store.clear()
        mutableState.value = UiState(coreAvailable = coreResult.isSuccess, busy = false)
    }

    fun clearMessage() {
        mutableState.value = mutableState.value.copy(error = null, notice = null)
    }

    private suspend fun loadAll(): List<Conversation> = withContext(Dispatchers.IO) {
        val all = mutableListOf<Conversation>()
        var cursor = ""
        repeat(100) {
            val page = parseConversationPage(core.listConversations(60, cursor))
            all += page.conversations
            if (page.nextCursor.isBlank() || page.nextCursor == cursor) return@withContext all
            cursor = page.nextCursor
        }
        error("Conversation pagination exceeded 100 pages")
    }

    private fun runTask(block: suspend () -> Unit) {
        mutableState.value = mutableState.value.copy(busy = true, error = null, notice = null)
        viewModelScope.launch(Dispatchers.IO) {
            runCatching { block() }
                .onFailure { failure ->
                    mutableState.value = mutableState.value.copy(error = failure.message ?: "The operation failed")
                }
            mutableState.value = mutableState.value.copy(busy = false)
        }
    }
}
