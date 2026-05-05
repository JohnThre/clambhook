package com.clambhook.android

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull

val ApiJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
}

@Serializable
data class StatusPayload(
    val running: Boolean = false,
    val profile: String = "",
    val listeners: List<ListenerStatusPayload> = emptyList()
)

@Serializable
data class ListenerStatusPayload(
    val protocol: String,
    val addr: String,
    @SerialName("active_conns")
    val activeConns: Int
)

@Serializable
data class ProfilesPayload(
    val profiles: List<String> = emptyList(),
    val active: String = ""
)

@Serializable
data class ServersPayload(
    val profile: String = "",
    val chains: List<ChainPayload> = emptyList()
)

@Serializable
data class ChainPayload(
    val name: String,
    val servers: List<ServerPayload>
)

@Serializable
data class ServerPayload(
    val name: String,
    val address: String,
    val protocol: String,
    val geo: LocationPayload = LocationPayload(),
    @SerialName("geo_error")
    val geoError: String? = null
)

@Serializable
data class LocationPayload(
    val country: String = "",
    @SerialName("country_code")
    val countryCode: String = "",
    val city: String = "",
    val latitude: Double = 0.0,
    val longitude: Double = 0.0
)

@Serializable
data class DaemonEvent(
    @SerialName("shard_id")
    val shardId: ULong,
    val lamport: ULong,
    @SerialName("ts_ns")
    val tsNs: Long,
    val type: String,
    val data: Map<String, JsonElement> = emptyMap()
)

data class BandwidthSample(
    val rxBps: Double = 0.0,
    val txBps: Double = 0.0
)

fun JsonElement.stringValueOrNull(): String? {
    val primitive = this as? JsonPrimitive ?: return null
    if (primitive is JsonNull) {
        return null
    }
    return primitive.content
}

fun JsonElement.doubleValueOrNull(): Double? {
    val primitive = this as? JsonPrimitive ?: return null
    return primitive.doubleOrNull
        ?: primitive.content.toDoubleOrNull()
        ?: primitive.booleanOrNull?.let { if (it) 1.0 else 0.0 }
}
