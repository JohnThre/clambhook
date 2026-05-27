package com.clambhook.android

import org.junit.Assert.assertEquals
import org.junit.Test

class SupportPurchaseModelsTest {
    @Test
    fun supportProductIdsAreStableAndOrdered() {
        assertEquals(
            listOf("support.small", "support.medium", "support.large"),
            supportProductIds
        )
        assertEquals(
            listOf("support.small", "support.large", "other"),
            orderedSupportProductIds(listOf("support.large", "other", "support.small"))
        )
    }
}
