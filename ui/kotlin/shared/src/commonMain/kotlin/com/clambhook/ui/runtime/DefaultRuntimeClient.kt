// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/** Default transport-neutral runtime client. */
class DefaultRuntimeClient(private val backend: Backend) : RuntimeClient {
    override suspend fun status() = Status.parse(backend.get("/api/v1/status"))
    override suspend fun profiles() = Profiles.parse(backend.get("/api/v1/profiles"))
    override suspend fun servers(profile: String) = getProfile("/api/v1/servers", profile)
    override suspend fun rules(profile: String) = getProfile("/api/v1/rules", profile)
    override suspend fun traffic(query: TrafficQuery) = get("/api/v1/traffic" + query.queryString())
    override suspend fun decisions(query: TrafficQuery) = get("/api/v1/decisions" + query.queryString())
    override suspend fun temporaryRules(profile: String) = getProfile("/api/v1/rules/temporary", profile)
    override suspend fun policyGroups(profile: String) = getProfile("/api/v1/policy-groups", profile)
    override suspend fun ruleSets(profile: String) = getProfile("/api/v1/rule-sets", profile)
    override suspend fun ruleSubscriptions(profile: String) = getProfile("/api/v1/rule-subscriptions", profile)
    override suspend fun dns(profile: String) = getProfile("/api/v1/dns", profile)
    override suspend fun settings(profile: String) = getProfile("/api/v1/config/settings", profile)
    override suspend fun conditioner(profile: String) = getProfile("/api/v1/conditioner", profile)
    override suspend fun pendingPrompts() = get("/api/v1/prompts/pending")
    override suspend fun promptDecisions() = get("/api/v1/prompts/decisions")
    override suspend fun developerStatus() = get("/api/v1/developer/status")

    override suspend fun developerEntries(query: String): Document {
        var suffix = query.trim()
        if (suffix.isNotEmpty() && suffix[0] != '?') suffix = "?$suffix"
        return get("/api/v1/developer/entries$suffix")
    }

    override suspend fun developerSettings() = get("/api/v1/developer/settings")
    override suspend fun developerMapRules(profile: String) = getProfile("/api/v1/developer/map-rules", profile)
    override suspend fun developerBreakpointRules(profile: String) =
        getProfile("/api/v1/developer/breakpoint-rules", profile)
    override suspend fun developerRewriteRules(profile: String) =
        getProfile("/api/v1/developer/rewrite-rules", profile)
    override suspend fun developerPendingBreakpoints() = get("/api/v1/developer/breakpoints/pending")
    override suspend fun developerCaPem() = backend.get("/api/v1/developer/ca.pem")

    override suspend fun events(cursor: EventCursor) =
        EventBatch.parse(backend.get("/api/v1/events/snapshot" + cursor.queryString()))

    override val supportsLiveEvents: Boolean get() = backend.supportsLiveEvents

    override fun liveEvents(cursor: EventCursor): Flow<Event> {
        if (!backend.supportsLiveEvents) {
            throw UnsupportedOperationException("this platform has no live event stream")
        }
        return backend.liveEvents("/api/v1/events" + cursor.queryString()).map(Event::parse)
    }

    override suspend fun rawRequest(method: String, path: String, body: String) =
        backend.request(method, path, body)

    private suspend fun get(path: String) = Document.parse(backend.get(path))

    private suspend fun getProfile(path: String, profile: String): Document {
        val value = profile.trim()
        return get(if (value.isEmpty()) path else "$path?profile=${formEncode(value)}")
    }

    override val displayName: String get() = backend.displayName

    override val supportsConnectionControl: Boolean get() = backend.supportsConnectionControl

    override val endpointSettings: EndpointSettings?
        get() = (backend as? ConfigurableEndpointBackend)?.let { EndpointSettings(it.baseUrl, "") }

    override fun configureEndpoint(settings: EndpointSettings) {
        val configurable = backend as? ConfigurableEndpointBackend
            ?: throw UnsupportedOperationException("this platform has no configurable endpoint")
        configurable.configure(settings.baseUrl, settings.bearerToken)
    }

    override fun close() = backend.close()
}
