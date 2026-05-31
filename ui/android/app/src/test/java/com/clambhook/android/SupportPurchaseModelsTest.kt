package com.clambhook.android

import org.junit.Assert.assertEquals
import org.junit.Test

class SupportPurchaseModelsTest {
    @Test
    fun supportProductIdsAreStableAndOrdered() {
        assertEquals(
            listOf(
                "org.jpfchang.clambhook.support.small",
                "org.jpfchang.clambhook.support.medium",
                "org.jpfchang.clambhook.support.large"
            ),
            supportProductIds
        )
        assertEquals(
            listOf(
                "org.jpfchang.clambhook.support.small",
                "org.jpfchang.clambhook.support.large",
                "other"
            ),
            orderedSupportProductIds(
                listOf(
                    "org.jpfchang.clambhook.support.large",
                    "other",
                    "org.jpfchang.clambhook.support.small"
                )
            )
        )
    }
}
