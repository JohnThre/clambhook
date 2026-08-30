// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui;

import com.clambhook.ui.platform.PlatformServices;
import com.clambhook.ui.runtime.RuntimeClient;
import javafx.application.Platform;
import javafx.scene.AccessibleRole;
import javafx.scene.Node;
import javafx.scene.Parent;
import javafx.scene.Scene;
import javafx.scene.control.Button;
import javafx.scene.control.ComboBoxBase;
import javafx.scene.control.ListView;
import javafx.scene.control.ScrollPane;
import javafx.scene.control.TabPane;
import javafx.scene.control.TableView;
import javafx.scene.control.TextInputControl;
import javafx.scene.input.KeyCode;
import javafx.scene.input.KeyCodeCombination;
import javafx.scene.input.KeyCombination;
import javafx.scene.layout.BorderPane;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.lang.reflect.Field;
import java.lang.reflect.Proxy;
import java.net.URL;
import java.util.ArrayList;
import java.util.Collections;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class MainViewTest {
    @BeforeAll
    static void startJavaFx() throws InterruptedException {
        CountDownLatch started = new CountDownLatch(1);
        Platform.startup(started::countDown);
        assertTrue(started.await(10, TimeUnit.SECONDS), "JavaFX toolkit did not start");
        Platform.setImplicitExit(false);
    }

    @AfterAll
    static void stopJavaFx() {
        Platform.exit();
    }

    @Test
    @SuppressWarnings("unchecked")
    void donationDestinationsAreExactAndProviderNeutral() throws Exception {
        Field field = MainView.class.getDeclaredField("DONATION_URLS");
        field.setAccessible(true);
        Map<String, String> links = (Map<String, String>) field.get(null);
        assertEquals(Map.of(
                "Ko-fi", "https://ko-fi.com/jpfchang",
                "Liberapay", "https://en.liberapay.com/jpfchang/",
                "IssueHunt", "https://oss.issuehunt.io/u/johnthre",
                "Donate crypto", "https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd"),
                links);
    }

    @Test
    void navigationIsResponsiveKeyboardAccessibleAndTouchSized() throws Exception {
        Fixture fixture = onFx(() -> fixture(false));
        try {
            flushFx();
            onFx(() -> {
                BorderPane root = (BorderPane) fixture.view().node();
                assertEquals(AccessibleRole.PARENT, root.getAccessibleRole());
                assertEquals("ClambHook network controller", root.getAccessibleText());
                assertNotNull(root.getLeft());
                assertNull(root.getBottom());

                List<Node> nodes = logicalNodes(root);
                List<Button> navigation = nodes.stream()
                        .filter(Button.class::isInstance)
                        .map(Button.class::cast)
                        .filter(button -> button.getStyleClass().contains("side-nav-button"))
                        .toList();
                assertEquals(9, navigation.size());
                assertTrue(navigation.stream().allMatch(button ->
                        button.getAccessibleText() != null
                                && button.getAccessibleText().startsWith("Open ")));

                assertSemanticLabels(nodes);
                root.applyCss();
                Button refresh = button(nodes, "Refresh");
                assertTrue(refresh.minHeight(-1) >= 48.0,
                        "interactive controls must retain a 48 px touch target");

                Runnable settingsShortcut = fixture.scene().getAccelerators().get(
                        new KeyCodeCombination(KeyCode.DIGIT9,
                                KeyCombination.SHORTCUT_DOWN));
                assertNotNull(settingsShortcut);
                settingsShortcut.run();
                assertEquals("Settings", nodes.stream()
                        .filter(node -> node.getStyleClass().contains("page-title"))
                        .map(node -> ((javafx.scene.control.Label) node).getText())
                        .findFirst().orElseThrow());

                root.resize(600, 760);
                assertNull(root.getLeft());
                assertNotNull(root.getBottom());
                root.resize(1180, 760);
                assertNotNull(root.getLeft());
                assertNull(root.getBottom());
                return null;
            });
        } finally {
            onFx(() -> {
                fixture.view().close();
                return null;
            });
        }
    }

    @Test
    void failedRefreshIsAnnouncedAndAUserRetryClearsIt() throws Exception {
        Fixture fixture = onFx(() -> fixture(true));
        try {
            flushFx();
            onFx(() -> {
                List<Node> nodes = logicalNodes(fixture.view().node());
                Node banner = nodes.stream()
                        .filter(node -> node.getStyleClass().contains("error-banner"))
                        .findFirst().orElseThrow();
                assertTrue(banner.isVisible());
                assertTrue(banner.getAccessibleText().startsWith("Error: "));
                button(nodes, "Refresh").fire();
                return null;
            });
            flushFx();
            flushFx();
            onFx(() -> {
                Node banner = logicalNodes(fixture.view().node()).stream()
                        .filter(node -> node.getStyleClass().contains("error-banner"))
                        .findFirst().orElseThrow();
                assertFalse(banner.isVisible());
                assertEquals("", banner.getAccessibleText());
                assertTrue(fixture.statusCalls().get() >= 2);
                return null;
            });
        } finally {
            onFx(() -> {
                fixture.view().close();
                return null;
            });
        }
    }

    @Test
    void corePaletteMeetsWcagContrastThresholds() {
        assertTrue(contrast("#edf4ff", "#0b1018") >= 7.0);
        assertTrue(contrast("#a9b8ca", "#0b1018") >= 4.5);
        assertTrue(contrast("#082016", "#5ee4a7") >= 4.5);
        assertTrue(contrast("#ffd3d3", "#4a2429") >= 4.5);
        URL stylesheet = ClambhookApplication.class.getResource("clambhook.css");
        assertNotNull(stylesheet);
    }

    private static void assertSemanticLabels(List<Node> nodes) {
        for (Node node : nodes) {
            if (node instanceof TextInputControl input) {
                assertFalse(blank(input.getAccessibleText()),
                        () -> "missing accessible text: " + input.getClass().getSimpleName());
            } else if (node instanceof ComboBoxBase<?> comboBox) {
                assertFalse(blank(comboBox.getAccessibleText()),
                        "combo box is missing accessible text");
            } else if (node instanceof TableView<?> table) {
                assertFalse(blank(table.getAccessibleText()),
                        "table is missing accessible text");
            } else if (node instanceof ListView<?> list) {
                assertFalse(blank(list.getAccessibleText()),
                        "list is missing accessible text");
            } else if (node instanceof Button button) {
                assertFalse(blank(button.getText()) && blank(button.getAccessibleText()),
                        "button is missing an accessible name");
            }
        }
    }

    private static boolean blank(String value) {
        return value == null || value.isBlank();
    }

    private static Button button(List<Node> nodes, String text) {
        return nodes.stream()
                .filter(Button.class::isInstance)
                .map(Button.class::cast)
                .filter(button -> text.equals(button.getText()))
                .findFirst().orElseThrow();
    }

    private static List<Node> logicalNodes(Node root) {
        List<Node> result = new ArrayList<>();
        Set<Node> seen = Collections.newSetFromMap(new IdentityHashMap<>());
        collect(root, result, seen);
        return result;
    }

    private static void collect(Node node, List<Node> result, Set<Node> seen) {
        if (node == null || !seen.add(node)) return;
        result.add(node);
        if (node instanceof Parent parent) {
            for (Node child : parent.getChildrenUnmodifiable()) {
                collect(child, result, seen);
            }
        }
        if (node instanceof ScrollPane scroll) {
            collect(scroll.getContent(), result, seen);
        }
        if (node instanceof TabPane tabs) {
            tabs.getTabs().forEach(tab -> collect(tab.getContent(), result, seen));
        }
    }

    private static Fixture fixture(boolean failFirstStatus) {
        AtomicInteger statusCalls = new AtomicInteger();
        RuntimeClient runtime = runtime(statusCalls, failFirstStatus);
        PlatformServices services = platformServices();
        MainView view = new MainView(runtime, services);
        Scene scene = new Scene(view.node(), 1180, 760);
        URL stylesheet = ClambhookApplication.class.getResource("clambhook.css");
        if (stylesheet != null) scene.getStylesheets().add(stylesheet.toExternalForm());
        view.installAccelerators(scene);
        return new Fixture(view, scene, statusCalls);
    }

    private static RuntimeClient runtime(
            AtomicInteger statusCalls, boolean failFirstStatus) {
        return (RuntimeClient) Proxy.newProxyInstance(
                RuntimeClient.class.getClassLoader(),
                new Class<?>[]{RuntimeClient.class},
                (proxy, method, arguments) -> switch (method.getName()) {
                    case "status" -> {
                        int call = statusCalls.incrementAndGet();
                        if (failFirstStatus && call == 1) {
                            yield CompletableFuture.failedFuture(
                                    new IllegalStateException("temporary API failure"));
                        }
                        yield CompletableFuture.completedFuture(RuntimeClient.Status.parse(
                                "{\"running\":false,\"listeners\":[]}"));
                    }
                    case "profiles" -> CompletableFuture.completedFuture(
                            RuntimeClient.Profiles.parse(
                                    "{\"active\":\"default\",\"profiles\":[\"default\"]}"));
                    case "servers", "rules", "traffic", "dns", "developerStatus",
                            "developerEntries" -> CompletableFuture.completedFuture(
                            RuntimeClient.Document.parse("{}"));
                    case "endpointSettings" -> Optional.empty();
                    case "displayName" -> "test C17 runtime";
                    case "supportsConnectionControl" -> true;
                    case "supportsLiveEvents" -> false;
                    case "close" -> null;
                    case "toString" -> "test runtime proxy";
                    default -> {
                        if (method.getReturnType() == CompletableFuture.class) {
                            yield CompletableFuture.completedFuture(
                                    RuntimeClient.Document.parse("{}"));
                        }
                        throw new UnsupportedOperationException(method.getName());
                    }
                });
    }

    private static PlatformServices platformServices() {
        return (PlatformServices) Proxy.newProxyInstance(
                PlatformServices.class.getClassLoader(),
                new Class<?>[]{PlatformServices.class},
                (proxy, method, arguments) -> switch (method.getName()) {
                    case "capabilities" -> Set.of();
                    case "supports" -> false;
                    case "requestVpnConsent" -> CompletableFuture.completedFuture(true);
                    case "startVpn", "stopVpn" -> CompletableFuture.completedFuture(null);
                    case "takePendingOutlineAccessKey" -> CompletableFuture.completedFuture("");
                    case "platformName" -> "test platform";
                    case "close" -> null;
                    case "toString" -> "test platform proxy";
                    default -> throw new UnsupportedOperationException(method.getName());
                });
    }

    private static double contrast(String foreground, String background) {
        double lighter = Math.max(luminance(foreground), luminance(background));
        double darker = Math.min(luminance(foreground), luminance(background));
        return (lighter + 0.05) / (darker + 0.05);
    }

    private static double luminance(String color) {
        int red = Integer.parseInt(color.substring(1, 3), 16);
        int green = Integer.parseInt(color.substring(3, 5), 16);
        int blue = Integer.parseInt(color.substring(5, 7), 16);
        return 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue);
    }

    private static double linear(int channel) {
        double value = channel / 255.0;
        return value <= 0.04045 ? value / 12.92
                : Math.pow((value + 0.055) / 1.055, 2.4);
    }

    private static void flushFx() throws Exception {
        onFx(() -> null);
    }

    private static <T> T onFx(Callable<T> operation) throws Exception {
        AtomicReference<T> value = new AtomicReference<>();
        AtomicReference<Throwable> failure = new AtomicReference<>();
        CountDownLatch completed = new CountDownLatch(1);
        Platform.runLater(() -> {
            try {
                value.set(operation.call());
            } catch (Throwable error) {
                failure.set(error);
            } finally {
                completed.countDown();
            }
        });
        assertTrue(completed.await(10, TimeUnit.SECONDS), "JavaFX operation timed out");
        Throwable error = failure.get();
        if (error instanceof Exception exception) throw exception;
        if (error != null) throw new AssertionError(error);
        return value.get();
    }

    private record Fixture(MainView view, Scene scene, AtomicInteger statusCalls) {
    }
}
