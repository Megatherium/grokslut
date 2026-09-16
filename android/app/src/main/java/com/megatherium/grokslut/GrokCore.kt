package com.megatherium.grokslut

import java.lang.reflect.InvocationTargetException
import java.lang.reflect.Method
import java.lang.reflect.Modifier

/** Keeps generated gomobile names out of the Kotlin source-level ABI. */
internal interface GrokCore {
    fun setSession(sessionJson: String)
    fun clearSession()
    fun listConversations(pageSize: Int, cursor: String): String
    fun exportConversations(idsJson: String, format: String, outDir: String): String

    companion object {
        fun load(): Result<GrokCore> = runCatching { ReflectedGrokCore() }
    }
}
private class ReflectedGrokCore : GrokCore {
    private val type = Class.forName("mobile.Mobile")

    override fun setSession(sessionJson: String) {
        invoke("sessionFromCookies", sessionJson)
    }

    override fun clearSession() {
        invoke("sessionClear")
    }

    override fun listConversations(pageSize: Int, cursor: String): String {
        val method = method("listConversations", 2)
        return invoke(method, numberFor(method.parameterTypes[0], pageSize), cursor) as String
    }

    override fun exportConversations(idsJson: String, format: String, outDir: String): String {
        return invoke("exportConversations", idsJson, format, outDir) as String
    }

    private fun invoke(name: String, vararg args: Any): Any? = invoke(method(name, args.size), *args)

    private fun invoke(method: Method, vararg args: Any): Any? {
        return try {
            val receiver = if (Modifier.isStatic(method.modifiers)) null else type.getDeclaredConstructor().newInstance()
            method.invoke(receiver, *args)
        } catch (error: InvocationTargetException) {
            throw (error.targetException ?: error)
        }
    }

    private fun method(name: String, arity: Int): Method = type.methods.firstOrNull {
        it.name.equals(name, ignoreCase = true) && it.parameterCount == arity
    } ?: error("grokcore is incompatible: missing $name/$arity")

    private fun numberFor(type: Class<*>, value: Int): Any = when (type) {
        java.lang.Long.TYPE, java.lang.Long::class.java -> value.toLong()
        else -> value
    }
}
