package com.clambhook.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TunnelRoutingTest {
    @Test
    fun parsesValidCidr() {
        assertEquals(RoutePrefix("0.0.0.0", 0), parseRoutePrefix("0.0.0.0/0"))
        assertEquals(RoutePrefix("10.0.0.0", 8), parseRoutePrefix("  10.0.0.0/8 "))
        assertEquals(RoutePrefix("::", 0), parseRoutePrefix("::/0"))
    }

    @Test
    fun rejectsMalformedCidr() {
        assertNull(parseRoutePrefix("10.0.0.0"))
        assertNull(parseRoutePrefix("10.0.0.0/"))
        assertNull(parseRoutePrefix("/8"))
        assertNull(parseRoutePrefix("10.0.0.0/abc"))
        assertNull(parseRoutePrefix(""))
    }

    @Test
    fun inverseRoutesSubtractsIpv4ExclusionFromDefault() {
        val result = inverseRoutes(listOf("0.0.0.0/0"), listOf("10.0.0.0/8"))
            .map { "${it.address}/${it.prefixLength}" }
            .toSet()

        // The eight sibling prefixes whose union is 0.0.0.0/0 minus 10.0.0.0/8.
        val expected = setOf(
            "128.0.0.0/1",
            "64.0.0.0/2",
            "32.0.0.0/3",
            "16.0.0.0/4",
            "0.0.0.0/5",
            "12.0.0.0/6",
            "8.0.0.0/7",
            "11.0.0.0/8",
        )
        assertEquals(expected, result)
    }

    @Test
    fun inverseRoutesCoversEverythingExceptTheExclusion() {
        val routes = inverseRoutes(listOf("0.0.0.0/0"), listOf("10.0.0.0/8"))
        assertTrue("11.0.0.0 must stay tunneled", covers(routes, "11.0.0.0"))
        assertTrue("9.255.255.255 must stay tunneled", covers(routes, "9.255.255.255"))
        assertTrue("192.168.1.1 must stay tunneled", covers(routes, "192.168.1.1"))
        assertTrue("0.0.0.0 must stay tunneled", covers(routes, "0.0.0.0"))
        assertTrue("255.255.255.255 must stay tunneled", covers(routes, "255.255.255.255"))
        assertTrue("10.0.0.1 must be excluded", !covers(routes, "10.0.0.1"))
        assertTrue("10.255.255.255 must be excluded", !covers(routes, "10.255.255.255"))
    }

    @Test
    fun inverseRoutesKeepsAddressFamiliesIndependent() {
        // An IPv4 exclusion must not perturb the IPv6 base and vice versa.
        val result = inverseRoutes(listOf("0.0.0.0/0", "::/0"), listOf("10.0.0.0/8"))
        assertTrue(result.any { prefixBytes(it).size == 16 && it.prefixLength == 0 })
        assertTrue(result.none { prefixBytes(it).size == 4 && it.prefixLength == 0 })
    }

    @Test
    fun inverseRoutesHandlesIpv6Exclusion() {
        val result = inverseRoutes(listOf("::/0"), listOf("::1/128"))
        assertEquals(128, result.size)
        assertTrue("::1 must be excluded", !covers(result, "::1"))
        assertTrue("::2 must stay tunneled", covers(result, "::2"))
        assertTrue("2001:db8::1 must stay tunneled", covers(result, "2001:db8::1"))
    }

    @Test
    fun inverseRoutesSkipsMalformedAndDisjointExclusions() {
        val result = inverseRoutes(listOf("192.168.0.0/16"), listOf("nonsense", "10.0.0.0/8"))
            .map { "${it.address}/${it.prefixLength}" }
        // 10/8 is disjoint from 192.168/16 and the garbage entry is skipped, so
        // the base is preserved unchanged.
        assertEquals(listOf("192.168.0.0/16"), result)
    }

    @Test
    fun resolveSplitTunnelDefaultsToOwnPackageOnly() {
        assertEquals(
            SplitTunnelPlan.DisallowOwnOnly,
            resolveSplitTunnel(SplitTunnelMode.All, setOf("com.other"), "com.clambhook.android"),
        )
    }

    @Test
    fun resolveSplitTunnelIncludeDropsOwnPackageAndSorts() {
        val plan = resolveSplitTunnel(
            SplitTunnelMode.Include,
            setOf("com.z", "com.a", "com.clambhook.android", " "),
            "com.clambhook.android",
        )
        assertEquals(SplitTunnelPlan.AllowOnly(listOf("com.a", "com.z")), plan)
    }

    @Test
    fun resolveSplitTunnelIncludeWithNoAppsFallsBackToFullTunnel() {
        val plan = resolveSplitTunnel(
            SplitTunnelMode.Include,
            setOf("com.clambhook.android"),
            "com.clambhook.android",
        )
        assertEquals(SplitTunnelPlan.DisallowOwnOnly, plan)
    }

    @Test
    fun resolveSplitTunnelExcludeKeepsSelection() {
        val plan = resolveSplitTunnel(
            SplitTunnelMode.Exclude,
            setOf("com.bank", "com.clambhook.android"),
            "com.clambhook.android",
        )
        assertEquals(SplitTunnelPlan.DisallowOwnAnd(listOf("com.bank")), plan)
    }

    @Test
    fun resolveSplitTunnelUnknownModeFallsBackToDefault() {
        assertEquals(
            SplitTunnelPlan.DisallowOwnOnly,
            resolveSplitTunnel("bogus", setOf("com.other"), "com.clambhook.android"),
        )
    }

    /** True when [ip] falls inside any of the given [routes]. */
    private fun covers(routes: List<RoutePrefix>, ip: String): Boolean {
        val target = java.net.InetAddress.getByName(ip).address
        return routes.any { route ->
            val net = java.net.InetAddress.getByName(route.address).address
            if (net.size != target.size) return@any false
            var remaining = route.prefixLength
            var index = 0
            while (remaining >= 8) {
                if (net[index] != target[index]) return@any false
                index++
                remaining -= 8
            }
            if (remaining == 0) return@any true
            val mask = (0xFF shl (8 - remaining)) and 0xFF
            (net[index].toInt() and mask) == (target[index].toInt() and mask)
        }
    }

    private fun prefixBytes(route: RoutePrefix): ByteArray =
        java.net.InetAddress.getByName(route.address).address
}
