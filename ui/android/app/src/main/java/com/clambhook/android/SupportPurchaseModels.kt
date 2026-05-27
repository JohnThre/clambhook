package com.clambhook.android

const val supportProductSmall = "support.small"
const val supportProductMedium = "support.medium"
const val supportProductLarge = "support.large"

val supportProductIds = listOf(
    supportProductSmall,
    supportProductMedium,
    supportProductLarge
)

data class SupportPurchaseProduct(
    val id: String,
    val name: String,
    val description: String,
    val price: String
)

data class SupportPurchaseState(
    val visible: Boolean = false,
    val loading: Boolean = false,
    val purchasing: Boolean = false,
    val products: List<SupportPurchaseProduct> = emptyList(),
    val statusMessage: String? = null
)

fun orderedSupportProductIds(ids: Iterable<String>): List<String> {
    val knownOrder = supportProductIds.withIndex().associate { it.value to it.index }
    return ids.sortedWith(compareBy({ knownOrder[it] ?: Int.MAX_VALUE }, { it }))
}
