package com.clambhook.android

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class DashboardViewModelTest {
    private val testDispatcher = StandardTestDispatcher()

    @Before
    fun setup() {
        Dispatchers.setMain(testDispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `given repository emits initial value when view model created then uiState starts empty`() = runTest {
        val repository = DashboardRepository(FakeApi())
        val viewModel = DashboardViewModel(repository, eventStream = null)

        advanceUntilIdle()

        assertEquals(DashboardState(), viewModel.uiState.value)
    }

    @Test
    fun `given repository refresh succeeds when collecting then uiState updates with repository state`() = runTest {
        val api = FakeApi(
            profiles = ProfilesPayload(profiles = listOf("A", "B"), active = "A")
        )
        val repository = DashboardRepository(api)
        val viewModel = DashboardViewModel(repository, eventStream = null)

        advanceUntilIdle()
        repository.refreshDashboard()
        advanceUntilIdle()

        assertEquals("A", viewModel.uiState.value.activeProfile)
        assertEquals(true, viewModel.uiState.value.apiOnline)
    }

    @Test
    fun `given repository refresh updates active profile when collecting then uiState reflects new value`() = runTest {
        val api = FakeApi(
            profiles = ProfilesPayload(profiles = listOf("A", "B"), active = "A")
        )
        val repository = DashboardRepository(api)
        val viewModel = DashboardViewModel(repository, eventStream = null)
        api.profiles = ProfilesPayload(profiles = listOf("A", "B"), active = "updated")

        advanceUntilIdle()
        repository.refreshDashboard()
        advanceUntilIdle()

        assertEquals("updated", viewModel.uiState.value.activeProfile)
        assertEquals(true, viewModel.uiState.value.apiOnline)
    }
}
