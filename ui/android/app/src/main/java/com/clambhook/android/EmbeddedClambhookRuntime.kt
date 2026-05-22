package com.clambhook.android

import java.lang.reflect.InvocationTargetException

interface EmbeddedClambhookRuntime {
    fun start(configPath: String, apiAddr: String, apiToken: String)
    fun stop()
    fun reload(configPath: String)
    fun isRunning(): Boolean
    fun validateConfig(configPath: String)
}

object GomobileClambhookRuntime : EmbeddedClambhookRuntime {
    private const val mobileClassName = "com.clambhook.mobile.Mobile"

    override fun start(configPath: String, apiAddr: String, apiToken: String) {
        invoke("start", configPath, apiAddr, apiToken)
    }

    override fun stop() {
        invoke("stop")
    }

    override fun reload(configPath: String) {
        invoke("reload", configPath)
    }

    override fun isRunning(): Boolean =
        invoke("isRunning") as? Boolean ?: false

    override fun validateConfig(configPath: String) {
        invoke("validateConfig", configPath)
    }

    private fun invoke(methodName: String, vararg args: String): Any? {
        val clazz = try {
            Class.forName(mobileClassName)
        } catch (error: ClassNotFoundException) {
            throw IllegalStateException("embedded clambhook runtime AAR is missing", error)
        }
        val parameterTypes = Array(args.size) { String::class.java }
        val method = clazz.getMethod(methodName, *parameterTypes)
        return try {
            method.invoke(null, *args)
        } catch (error: InvocationTargetException) {
            throw error.targetException ?: error
        }
    }
}
