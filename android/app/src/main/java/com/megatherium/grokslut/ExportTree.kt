package com.megatherium.grokslut

import android.content.Context
import android.net.Uri
import android.webkit.MimeTypeMap
import androidx.documentfile.provider.DocumentFile
import java.io.File

internal fun copyExportToTree(context: Context, source: File, treeUri: Uri) {
    val destination = DocumentFile.fromTreeUri(context, treeUri) ?: error("The selected folder is unavailable")
    source.listFiles().orEmpty().forEach { copyEntry(context, it, destination) }
}

private fun copyEntry(context: Context, source: File, parent: DocumentFile) {
    if (source.isDirectory) {
        val directory = parent.findFile(source.name) ?: parent.createDirectory(source.name)
        requireNotNull(directory) { "Could not create ${source.name}" }
        source.listFiles().orEmpty().forEach { copyEntry(context, it, directory) }
        return
    }
    parent.findFile(source.name)?.delete()
    val target = parent.createFile(mimeType(source), source.name)
    requireNotNull(target) { "Could not create ${source.name}" }
    context.contentResolver.openOutputStream(target.uri, "w").use { output ->
        requireNotNull(output) { "Could not open ${source.name}" }
        source.inputStream().use { input -> input.copyTo(output) }
    }
}

private fun mimeType(file: File): String {
    val extension = file.extension.lowercase()
    return MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension)
        ?: when (extension) {
            "md" -> "text/markdown"
            "json" -> "application/json"
            "zip" -> "application/zip"
            else -> "application/octet-stream"
        }
}
