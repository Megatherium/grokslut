package com.megatherium.grokslut

import org.json.JSONArray
import org.json.JSONObject

internal data class Conversation(
    val id: String,
    val title: String,
    val updatedAt: String,
    val projectId: String,
    val projectTitle: String,
)

internal data class ConversationPage(val conversations: List<Conversation>, val nextCursor: String)

internal fun parseConversationPage(raw: String): ConversationPage {
    val root = JSONObject(raw)
    val items = root.optJSONArray("conversations") ?: JSONArray()
    val conversations = buildList {
        for (index in 0 until items.length()) {
            val item = items.optJSONObject(index) ?: continue
            val id = item.optString("id")
            if (id.isBlank()) continue
            add(
                Conversation(
                    id = id,
                    title = item.optString("title", "Untitled conversation"),
                    updatedAt = item.optString("updatedAt"),
                    projectId = item.optString("projectId"),
                    projectTitle = item.optString("projectTitle"),
                ),
            )
        }
    }
    return ConversationPage(conversations, root.optString("nextCursor"))
}

internal fun idsJson(ids: Collection<String>): String = JSONArray(ids).toString()
