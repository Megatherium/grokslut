package com.megatherium.grokslut

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme { GrokslutApp() }
        }
    }
}

@Composable
private fun GrokslutApp(model: AppViewModel = viewModel()) {
    val state by model.state.collectAsState()
    var showLogin by remember { mutableStateOf(false) }
    var showFormat by remember { mutableStateOf(false) }
    var pendingFormat by remember { mutableStateOf("zip") }

    val importSession = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        uri ?: return@rememberLauncherForActivityResult
        runCatching {
            val resolver = model.getApplication<android.app.Application>().contentResolver
            resolver.openInputStream(uri)?.bufferedReader()?.use { it.readText() }
                ?: error("Could not read the selected file")
        }.onSuccess(model::importSession)
    }
    val exportTree = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        uri ?: return@rememberLauncherForActivityResult
        runCatching {
            model.getApplication<android.app.Application>().contentResolver.takePersistableUriPermission(
                uri,
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
            )
        }
        model.export(pendingFormat, uri)
    }

    if (showLogin) {
        LoginWebView(
            onCancel = { showLogin = false },
            onSession = {
                showLogin = false
                model.connect(it)
            },
        )
        return
    }

    Scaffold { padding ->
        Column(Modifier.fillMaxSize().padding(padding).padding(16.dp)) {
            Text("grokslut", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
            Text("Personal Grok conversation export", color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.padding(8.dp))

            state.error?.let { Message(it, true, model::clearMessage) }
            state.notice?.let { Message(it, false, model::clearMessage) }

            when {
                !state.coreAvailable -> MissingCore()
                !state.signedIn -> SignedOut(
                    onLogin = { showLogin = true },
                    onImport = { importSession.launch("application/json") },
                )
                else -> ConversationScreen(
                    state = state,
                    onRefresh = model::refresh,
                    onToggle = model::toggle,
                    onSelectAll = model::selectAll,
                    onExport = { showFormat = true },
                    onSignOut = model::signOut,
                )
            }
        }
        if (state.busy) BusyOverlay()
    }

    if (showFormat) {
        FormatDialog(
            initial = pendingFormat,
            onDismiss = { showFormat = false },
            onConfirm = {
                pendingFormat = it
                showFormat = false
                exportTree.launch(null)
            },
        )
    }
}

@Composable
private fun SignedOut(onLogin: () -> Unit, onImport: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("No Grok session is connected.", style = MaterialTheme.typography.titleMedium)
        Text(
            "An app-owned WebView can reuse its own cookies without root. Embedded Google sign-in may be refused by Google; Grok/X login or importing a session remains available.",
        )
        Button(onClick = onLogin, modifier = Modifier.fillMaxWidth()) { Text("Sign in to Grok") }
        Button(onClick = onImport, modifier = Modifier.fillMaxWidth()) { Text("Import session JSON") }
    }
}

@Composable
private fun MissingCore() {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("The Go core is missing from this development build.", color = MaterialTheme.colorScheme.error)
        Text("Run scripts/build-android.sh before installing the APK.")
    }
}

@Composable
private fun ConversationScreen(
    state: UiState,
    onRefresh: () -> Unit,
    onToggle: (String) -> Unit,
    onSelectAll: () -> Unit,
    onExport: () -> Unit,
    onSignOut: () -> Unit,
) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxWidth()) {
        TextButton(onClick = onRefresh) { Text("Refresh") }
        TextButton(onClick = onSelectAll) { Text(if (state.selected.size == state.conversations.size) "Clear" else "Select all") }
        Spacer(Modifier.weight(1f))
        TextButton(onClick = onSignOut) { Text("Sign out") }
    }
    Text("${state.selected.size} selected · ${state.conversations.size} conversations")
    Button(
        onClick = onExport,
        enabled = state.selected.isNotEmpty() && !state.busy,
        modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
    ) { Text("Export selected") }

    val groups = state.conversations.groupBy {
        it.projectTitle.ifBlank { if (it.projectId.isBlank()) "Conversations" else "Project ${it.projectId}" }
    }.toSortedMap()
    LazyColumn(Modifier.fillMaxSize()) {
        groups.forEach { (project, conversations) ->
            item(key = "project:$project") {
                Text(
                    project,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.fillMaxWidth().padding(top = 16.dp, bottom = 4.dp),
                )
            }
            items(conversations, key = { it.id }) { conversation ->
                Row(
                    modifier = Modifier.fillMaxWidth().clickable { onToggle(conversation.id) }.padding(vertical = 6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Checkbox(
                        checked = conversation.id in state.selected,
                        onCheckedChange = { onToggle(conversation.id) },
                    )
                    Spacer(Modifier.width(8.dp))
                    Column {
                        Text(conversation.title)
                        if (conversation.updatedAt.isNotBlank()) {
                            Text(conversation.updatedAt, style = MaterialTheme.typography.bodySmall)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun FormatDialog(initial: String, onDismiss: () -> Unit, onConfirm: (String) -> Unit) {
    var format by remember(initial) { mutableStateOf(initial) }
    val choices = listOf("markdown" to "Markdown + media", "json" to "JSON", "zip" to "ZIP bundle")
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Export format") },
        text = {
            Column {
                choices.forEach { (value, title) ->
                    Row(
                        Modifier.fillMaxWidth().clickable { format = value }.padding(vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(selected = format == value, onClick = { format = value })
                        Text(title)
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = { onConfirm(format) }) { Text("Choose folder") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
private fun Message(text: String, error: Boolean, onDismiss: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(text, color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary, modifier = Modifier.weight(1f))
        TextButton(onClick = onDismiss) { Text("Dismiss") }
    }
}

@Composable
private fun BusyOverlay() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}
