package com.clambhook.android

import android.app.Activity
import android.content.Context
import com.android.billingclient.api.BillingClient
import com.android.billingclient.api.BillingClient.BillingResponseCode
import com.android.billingclient.api.BillingClientStateListener
import com.android.billingclient.api.BillingFlowParams
import com.android.billingclient.api.BillingResult
import com.android.billingclient.api.ConsumeParams
import com.android.billingclient.api.PendingPurchasesParams
import com.android.billingclient.api.ProductDetails
import com.android.billingclient.api.Purchase
import com.android.billingclient.api.PurchasesUpdatedListener
import com.android.billingclient.api.QueryProductDetailsParams
import com.android.billingclient.api.QueryPurchasesParams
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

class SupportPurchaseManager(context: Context) : PurchasesUpdatedListener {
    private val productDetails = mutableMapOf<String, ProductDetails>()
    private val appContext = context.applicationContext
    private var connecting = false

    private val _state = MutableStateFlow(SupportPurchaseState(visible = true, loading = true))
    val state: StateFlow<SupportPurchaseState> = _state.asStateFlow()

    private val billingClient = BillingClient.newBuilder(appContext)
        .setListener(this)
        .enablePendingPurchases(PendingPurchasesParams.newBuilder().enableOneTimeProducts().build())
        .enableAutoServiceReconnection()
        .build()

    fun start() {
        if (billingClient.isReady) {
            refresh()
            return
        }
        if (connecting) {
            return
        }
        connecting = true
        _state.update { it.copy(loading = true, statusMessage = null) }
        billingClient.startConnection(object : BillingClientStateListener {
            override fun onBillingSetupFinished(billingResult: BillingResult) {
                connecting = false
                if (billingResult.responseCode == BillingResponseCode.OK) {
                    refresh()
                    processOutstandingPurchases()
                } else {
                    _state.update {
                        it.copy(
                            loading = false,
                            statusMessage = billingResult.debugMessage.ifBlank { "Support purchases unavailable" }
                        )
                    }
                }
            }

            override fun onBillingServiceDisconnected() {
                connecting = false
                _state.update { it.copy(loading = false, statusMessage = "Support purchases disconnected") }
            }
        })
    }

    fun refresh() {
        if (!billingClient.isReady) {
            start()
            return
        }

        _state.update { it.copy(loading = true, statusMessage = null) }
        val params = QueryProductDetailsParams.newBuilder()
            .setProductList(
                supportProductIds.map { productId ->
                    QueryProductDetailsParams.Product.newBuilder()
                        .setProductId(productId)
                        .setProductType(BillingClient.ProductType.INAPP)
                        .build()
                }
            )
            .build()

        billingClient.queryProductDetailsAsync(params) { billingResult, result ->
            if (billingResult.responseCode != BillingResponseCode.OK) {
                _state.update {
                    it.copy(
                        loading = false,
                        statusMessage = billingResult.debugMessage.ifBlank { "Support products unavailable" }
                    )
                }
                return@queryProductDetailsAsync
            }

            productDetails.clear()
            result.productDetailsList.forEach { details ->
                productDetails[details.productId] = details
            }
            val byId = result.productDetailsList.associateBy { it.productId }
            val products = orderedSupportProductIds(byId.keys).mapNotNull { productId ->
                byId[productId]?.toSupportProduct()
            }
            _state.update {
                it.copy(
                    loading = false,
                    products = products,
                    statusMessage = if (products.isEmpty()) "Configure support products in Play Console" else null
                )
            }
        }
    }

    fun purchase(activity: Activity, productId: String) {
        val details = productDetails[productId]
        if (details == null) {
            _state.update { it.copy(statusMessage = "Support product unavailable") }
            refresh()
            return
        }

        val detailsParams = BillingFlowParams.ProductDetailsParams.newBuilder()
            .setProductDetails(details)
            .apply {
                details.firstOffer()?.offerToken?.takeIf { it.isNotBlank() }?.let { setOfferToken(it) }
            }
            .build()
        val flowParams = BillingFlowParams.newBuilder()
            .setProductDetailsParamsList(listOf(detailsParams))
            .build()
        _state.update { it.copy(purchasing = true, statusMessage = null) }
        val result = billingClient.launchBillingFlow(activity, flowParams)
        if (result.responseCode != BillingResponseCode.OK) {
            _state.update {
                it.copy(
                    purchasing = false,
                    statusMessage = result.debugMessage.ifBlank { "Support purchase could not start" }
                )
            }
        }
    }

    fun clearMessage() {
        _state.update { it.copy(statusMessage = null) }
    }

    fun close() {
        if (billingClient.isReady) {
            billingClient.endConnection()
        }
    }

    override fun onPurchasesUpdated(billingResult: BillingResult, purchases: MutableList<Purchase>?) {
        when (billingResult.responseCode) {
            BillingResponseCode.OK -> {
                val supportPurchases = purchases.orEmpty().filter { purchase ->
                    purchase.products.any { it in supportProductIds }
                }
                if (supportPurchases.isEmpty()) {
                    _state.update { it.copy(purchasing = false) }
                } else {
                    supportPurchases.forEach(::processPurchase)
                }
            }
            BillingResponseCode.USER_CANCELED -> {
                _state.update { it.copy(purchasing = false, statusMessage = "Support purchase cancelled") }
            }
            else -> {
                _state.update {
                    it.copy(
                        purchasing = false,
                        statusMessage = billingResult.debugMessage.ifBlank { "Support purchase failed" }
                    )
                }
            }
        }
    }

    private fun processOutstandingPurchases() {
        val params = QueryPurchasesParams.newBuilder()
            .setProductType(BillingClient.ProductType.INAPP)
            .build()
        billingClient.queryPurchasesAsync(params) { billingResult, purchases ->
            if (billingResult.responseCode == BillingResponseCode.OK) {
                purchases
                    .filter { purchase -> purchase.products.any { it in supportProductIds } }
                    .forEach(::processPurchase)
            }
        }
    }

    private fun processPurchase(purchase: Purchase) {
        if (purchase.purchaseState == Purchase.PurchaseState.PENDING) {
            _state.update { it.copy(purchasing = false, statusMessage = "Support purchase pending approval") }
            return
        }
        if (purchase.purchaseState != Purchase.PurchaseState.PURCHASED) {
            _state.update { it.copy(purchasing = false) }
            return
        }

        val params = ConsumeParams.newBuilder()
            .setPurchaseToken(purchase.purchaseToken)
            .build()
        billingClient.consumeAsync(params) { billingResult, _ ->
            _state.update {
                it.copy(
                    purchasing = false,
                    statusMessage = if (billingResult.responseCode == BillingResponseCode.OK) {
                        "Thanks for supporting clambhook"
                    } else {
                        billingResult.debugMessage.ifBlank { "Support purchase processing failed" }
                    }
                )
            }
        }
    }

    private fun ProductDetails.toSupportProduct(): SupportPurchaseProduct? {
        val offer = firstOffer() ?: return null
        return SupportPurchaseProduct(
            id = productId,
            name = name.ifBlank { title },
            description = description,
            price = offer.formattedPrice
        )
    }

    private fun ProductDetails.firstOffer(): ProductDetails.OneTimePurchaseOfferDetails? {
        return oneTimePurchaseOfferDetails ?: oneTimePurchaseOfferDetailsList?.firstOrNull()
    }
}
