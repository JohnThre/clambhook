package com.clambhook.android

import android.app.Activity
import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class SupportPurchaseManager(context: Context) {
    @Suppress("unused")
    private val appContext = context.applicationContext
    private val _state = MutableStateFlow(SupportPurchaseState())
    val state: StateFlow<SupportPurchaseState> = _state.asStateFlow()

    fun start() = Unit

    fun refresh() = Unit

    fun purchase(activity: Activity, productId: String) = Unit

    fun clearMessage() = Unit

    fun close() = Unit
}
