package com.clambhook.android

import java.net.InetAddress

/**
 * Pure (Android-framework-free) routing helpers shared by [ClambhookVpnService].
 *
 * Extracted so the CIDR parsing, the Android 11/12 inverse-route fallback, and
 * the split-tunnel selection can be unit tested on the JVM without a device or
 * the VpnService.Builder.
 */

/** A parsed CIDR route: a literal address plus its prefix length. */
data class RoutePrefix(val address: String, val prefixLength: Int)

/**
 * Splits a CIDR string ("0.0.0.0/0") into an address and prefix length.
 * Returns null for malformed input; callers log as appropriate.
 */
fun parseRoutePrefix(cidr: String): RoutePrefix? {
    val trimmed = cidr.trim()
    val slash = trimmed.indexOf('/')
    if (slash <= 0 || slash == trimmed.length - 1) return null
    val prefix = trimmed.substring(slash + 1).toIntOrNull() ?: return null
    if (prefix < 0) return null
    return RoutePrefix(trimmed.substring(0, slash), prefix)
}

/**
 * Computes the concrete route set covering [included] minus [excluded].
 *
 * Android 12 and below do not expose `VpnService.Builder.excludeRoute`, so
 * exclusions are honored by installing the inverse route set with `addRoute`:
 * each excluded prefix is subtracted from its enclosing included prefix by
 * splitting the enclosing prefix into the sibling prefixes that do not contain
 * the exclusion. The union of the returned prefixes equals `included` with the
 * `excluded` ranges removed. Malformed routes are skipped. Address families are
 * subtracted independently (an IPv4 exclusion never affects an IPv6 base).
 */
fun inverseRoutes(included: List<String>, excluded: List<String>): List<RoutePrefix> {
    val includedBlocks = included.mapNotNull(::toIpBlock)
    val excludedBlocks = excluded.mapNotNull(::toIpBlock)
    val result = mutableListOf<IpBlock>()
    for (base in includedBlocks) {
        var remaining = listOf(base)
        for (exclude in excludedBlocks) {
            if (exclude.bits != base.bits) continue
            remaining = remaining.flatMap { block ->
                when {
                    block.contains(exclude) -> block.subtract(exclude)
                    exclude.contains(block) -> emptyList()
                    else -> listOf(block)
                }
            }
        }
        result += remaining
    }
    return result.map { it.toRoutePrefix() }
}

/** The set of packages a per-app routing mode resolves to. */
sealed interface SplitTunnelPlan {
    /** Only [packages] traverse the tunnel (include mode). */
    data class AllowOnly(val packages: List<String>) : SplitTunnelPlan

    /** Everything except the app's own package and [packages] traverses the tunnel. */
    data class DisallowOwnAnd(val packages: List<String>) : SplitTunnelPlan

    /** Full tunnel with only the app's own egress excluded (default). */
    data object DisallowOwnOnly : SplitTunnelPlan
}

/**
 * Resolves the per-app routing selection into a concrete plan. The app's own
 * package is always kept outside the tunnel so its proxy sockets do not loop
 * back; an empty include selection falls back to a full tunnel.
 */
fun resolveSplitTunnel(
    mode: String,
    selectedPackages: Set<String>,
    ownPackage: String,
): SplitTunnelPlan {
    val selected = selectedPackages
        .map { it.trim() }
        .filter { it.isNotBlank() && it != ownPackage }
        .toSortedSet()
        .toList()
    return when (mode.takeIf { it in SplitTunnelMode.supported } ?: SplitTunnelMode.All) {
        SplitTunnelMode.Include ->
            if (selected.isEmpty()) SplitTunnelPlan.DisallowOwnOnly else SplitTunnelPlan.AllowOnly(selected)
        SplitTunnelMode.Exclude -> SplitTunnelPlan.DisallowOwnAnd(selected)
        else -> SplitTunnelPlan.DisallowOwnOnly
    }
}

/** A network-masked IP prefix used for inverse-route arithmetic. */
private class IpBlock(bytes: ByteArray, val prefixLength: Int) {
    val bits: Int = bytes.size * 8
    val bytes: ByteArray = maskBytes(bytes, prefixLength)

    fun contains(other: IpBlock): Boolean {
        if (bits != other.bits || prefixLength > other.prefixLength) return false
        return firstBitsEqual(bytes, other.bytes, prefixLength)
    }

    /** base minus [exclude], where [exclude] is contained in this block. */
    fun subtract(exclude: IpBlock): List<IpBlock> {
        val siblings = mutableListOf<IpBlock>()
        for (i in prefixLength until exclude.prefixLength) {
            val sibling = exclude.bytes.copyOf()
            flipBit(sibling, i)
            siblings += IpBlock(sibling, i + 1)
        }
        return siblings
    }

    fun toRoutePrefix(): RoutePrefix =
        RoutePrefix(InetAddress.getByAddress(bytes).hostAddress ?: "", prefixLength)
}

private fun toIpBlock(cidr: String): IpBlock? {
    val parsed = parseRoutePrefix(cidr) ?: return null
    val bytes = runCatching { InetAddress.getByName(parsed.address).address }.getOrNull() ?: return null
    if (parsed.prefixLength > bytes.size * 8) return null
    return IpBlock(bytes, parsed.prefixLength)
}

private fun maskBytes(bytes: ByteArray, prefixLength: Int): ByteArray {
    val out = bytes.copyOf()
    for (i in prefixLength until out.size * 8) {
        val byteIndex = i / 8
        val bitPos = 7 - (i % 8)
        out[byteIndex] = (out[byteIndex].toInt() and (1 shl bitPos).inv()).toByte()
    }
    return out
}

private fun flipBit(bytes: ByteArray, index: Int) {
    val byteIndex = index / 8
    val bitPos = 7 - (index % 8)
    bytes[byteIndex] = (bytes[byteIndex].toInt() xor (1 shl bitPos)).toByte()
}

private fun firstBitsEqual(a: ByteArray, b: ByteArray, count: Int): Boolean {
    var remaining = count
    var index = 0
    while (remaining >= 8) {
        if (a[index] != b[index]) return false
        index++
        remaining -= 8
    }
    if (remaining == 0) return true
    val mask = (0xFF shl (8 - remaining)) and 0xFF
    return (a[index].toInt() and mask) == (b[index].toInt() and mask)
}
