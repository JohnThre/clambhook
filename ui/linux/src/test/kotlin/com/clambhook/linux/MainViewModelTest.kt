// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.linux.ui

import com.clambhook.linux.api.ClambhookApi
import com.clambhook.linux.api.ClambhookApiClient
import com.clambhook.linux.daemon.DaemonSupervisor
import com.clambhook.linux.event.EventStream
import com.clambhook.linux.license.LicenseHelperClient
import com.clambhook.linux.license.LicenseKeyVault
import com.clambhook.linux.license.LicenseManager
import com.clambhook.linux.license.LicensePersistedState
import com.clambhook.linux.license.LicenseStateStore
import com.clambhook.linux.model.*
import com.clambhook.linux.settings.AppSettings
import com.clambhook.linux.settings.FileSettingsStore
import com.clambhook.linux.settings.TokenVault
import com.clambhook.linux.store.DashboardStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.Test
import kotlin.test.assertEquals

@OptIn(ExperimentalCoroutinesApi::class)
class MainViewModelTest {
    private class FakeApi : ClambhookApi {
        override suspend fun status(): StatusPayload = StatusPayload()

        override suspend fun profiles(): ProfilesPayload = ProfilesPayload()

        override suspend fun servers(): ServersPayload = ServersPayload()

        override suspend fun rules(): RulesPayload = RulesPayload()

        override suspend fun traffic(): TrafficSnapshotPayload = TrafficSnapshotPayload()

        override suspend fun connect() {}

        override suspend fun disconnect() {}

        override suspend fun setActiveProfile(name: String) {}

        override suspend fun createRule(rule: RulePayload): RulesPayload = RulesPayload()

        override suspend fun createRuleFromConnection(
            connection: TrafficConnectionPayload,
            rule: RulePayload,
        ): RulesPayload = RulesPayload()

        override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload = RulesPayload()

        override suspend fun policyGroups(): PolicyGroupsPayload = PolicyGroupsPayload()

        override suspend fun selectPolicyGroup(
            group: String,
            chain: String,
        ): PolicyGroupsPayload = PolicyGroupsPayload()

        override suspend fun testPolicyGroups(group: String): PolicyGroupsPayload = PolicyGroupsPayload()

        override suspend fun pendingPrompts(): PromptsPayload = PromptsPayload()

        override suspend fun resolvePrompt(
            id: String,
            action: String,
            scope: String,
            matchHost: Boolean,
        ) {}

        override suspend fun dns(): DnsPayload = DnsPayload()

        override suspend fun developerStatus(): DeveloperStatusPayload = DeveloperStatusPayload()

        override suspend fun setDeveloperCapture(enabled: Boolean): DeveloperStatusPayload = DeveloperStatusPayload()

        override suspend fun developerEntries(): List<DeveloperEntryPayload> = emptyList()

        override suspend fun developerEntries(filter: DeveloperEntriesFilter): List<DeveloperEntryPayload> = emptyList()

        override suspend fun developerEntry(id: String): DeveloperEntryPayload = DeveloperEntryPayload()

        override suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload = DeveloperEntryPayload()

        override suspend fun developerEntryCurl(id: String): String = ""

        override suspend fun importCurl(text: String): ParsedCurlResponse = ParsedCurlResponse()

        override suspend fun sendComposed(request: ComposedRequestPayload): DeveloperEntryPayload = DeveloperEntryPayload()

        override suspend fun conditioner(profile: String): ConditionerPayload = ConditionerPayload()

        override suspend fun updateConditioner(request: ConditionerUpdateRequest): ConditionerPayload = ConditionerPayload()

        override fun eventsUri(): String = "ws://127.0.0.1:9090/api/v1/events"

        override fun authorizationHeader(): String = ""

        var configuredBaseUrl: String = ""

        override fun configureBaseUrl(baseUrl: String) {
            configuredBaseUrl = baseUrl
        }
    }

    private class FakeTokenVault : TokenVault {
        var savedToken: String = ""
        var savedCount = 0

        override suspend fun readToken(): String = "initial-token"

        override suspend fun saveToken(token: String) {
            savedToken = token
            savedCount++
        }
    }

    private class FakeLicenseStateStore : LicenseStateStore {
        override fun load(): LicensePersistedState = LicensePersistedState()

        override fun save(state: LicensePersistedState) {}

        override fun daemonSnapshotPath(): String = ""
    }

    private class FakeLicenseKeyVault : LicenseKeyVault {
        override suspend fun readLicenseKey(): String = ""

        override suspend fun saveLicenseKey(licenseKey: String) {}
    }

    private class FakeLicenseHelper : LicenseHelperClient("") {
        override suspend fun call(
            command: String,
            request: kotlinx.serialization.json.JsonObject,
        ): String = ""
    }

    private class NoOpEventStream : EventStream {
        override var onEvent: ((DaemonEvent) -> Unit)? = null
        override var onFailed: ((String) -> Unit)? = null
        override var onClosed: (() -> Unit)? = null

        override fun start(
            uri: String,
            authorization: String,
        ) {}

        override fun stop() {}
    }

    private fun TestScope.createViewModel(
        tokenRef: AtomicReference<String>,
        api: ClambhookApi,
        settingsStore: FileSettingsStore,
        tokenVault: TokenVault,
        license: LicenseManager = LicenseManager(FakeLicenseStateStore(), FakeLicenseKeyVault(), FakeLicenseHelper()),
    ): MainViewModel =
        MainViewModel(
            store = DashboardStore(api),
            client = api,
            settingsStore = settingsStore,
            tokenVault = tokenVault,
            daemon = DaemonSupervisor(),
            license = license,
            initialSettings = AppSettings(),
            apiTokenRef = tokenRef,
            eventStream = NoOpEventStream(),
            dispatcher = Dispatchers.Unconfined,
        )

    @Test
    fun saveSettingsAppliesCurrentStateImmediately() =
        runTest {
            val tokenRef = AtomicReference("")
            val api = FakeApi()
            val tmp =
                java.nio.file.Files
                    .createTempDirectory("vm-test")
            val settingsStore = FileSettingsStore(tmp.resolve("settings.json"))
            val tokenVault = FakeTokenVault()
            val vm = createViewModel(tokenRef, api, settingsStore, tokenVault)
            advanceUntilIdle()

            val endpoint = "https://proxy.example:9443"
            vm.saveSettings(AppSettings(apiEndpoint = endpoint), "new-token")
            assertEquals(endpoint, vm.settings.value.apiEndpoint)
            assertEquals(endpoint, api.configuredBaseUrl)
            assertEquals("new-token", tokenRef.get())
            assertEquals("new-token", vm.apiToken)

            advanceUntilIdle()
            assertEquals(endpoint, settingsStore.current().apiEndpoint)
            assertEquals("new-token", tokenVault.savedToken)
            vm.close()
        }

    @Test
    fun saveSettingsRejectsEndpointWithPathAndFallsBack() =
        runTest {
            val tokenRef = AtomicReference("")
            val api = FakeApi()
            val tmp =
                java.nio.file.Files
                    .createTempDirectory("vm-test")
            val settingsStore = FileSettingsStore(tmp.resolve("settings.json"))
            val tokenVault = FakeTokenVault()
            val vm = createViewModel(tokenRef, api, settingsStore, tokenVault)
            advanceUntilIdle()
            vm.saveSettings(AppSettings(apiEndpoint = "http://proxy.example:9090/api"), "new-token")
            advanceUntilIdle()
            assertEquals("http://127.0.0.1:9090", vm.settings.value.apiEndpoint)
            assertEquals("http://127.0.0.1:9090", api.configuredBaseUrl)
            assertEquals("new-token", tokenVault.savedToken)
            vm.close()
        }

    @Test
    fun saveSettingsStoresCurrentTokenImmediately() =
        runTest {
            val tokenRef = AtomicReference("")
            val api = FakeApi()
            val vm =
                createViewModel(
                    tokenRef,
                    api,
                    object : FileSettingsStore() {
                        override fun save(settings: AppSettings) {}
                    },
                    object : TokenVault {
                        override suspend fun readToken(): String = ""

                        override suspend fun saveToken(token: String) {}
                    },
                )
            advanceUntilIdle()
            vm.saveSettings(AppSettings(), "  new-token  ")
            assertEquals("new-token", vm.apiToken)
            assertEquals("new-token", tokenRef.get())
            vm.close()
        }

    @Test
    fun baseUrlReconfigurationIsAtomic() {
        val ref = AtomicReference("")
        val client = ClambhookApiClient("http://127.0.0.1:9090") { ref.get() }
        client.configureBaseUrl("http://proxy.example:9090")
        assertEquals(
            "proxy.example:9090",
            client.eventsUri().removePrefix("ws://").removeSuffix("/api/v1/events?types=connection.*,rule.*,hop.*,log.*"),
        )
    }

    @Test
    fun eventsUriSupportsHttpsAndPaths() {
        val client = ClambhookApiClient("https://proxy.example:9443") { "" }
        assertEquals("wss://proxy.example:9443/api/v1/events?types=connection.*,rule.*,hop.*,log.*", client.eventsUri())
    }

    @Test
    fun atomicTokenUpdatesVisibleToProvider() {
        val ref = AtomicReference("")
        val client = ClambhookApiClient("http://127.0.0.1:9090") { ref.get() }
        assertEquals("", client.authorizationHeader())
        ref.set("tok")
        assertEquals("Bearer tok", client.authorizationHeader())
    }
}
