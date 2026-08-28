// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui;

import com.clambhook.ui.backend.Backend;
import com.clambhook.ui.backend.HttpBackend;
import com.clambhook.ui.format.Formatters;
import com.clambhook.ui.json.Json;
import com.clambhook.ui.model.DashboardData;
import com.clambhook.ui.model.DashboardLoader;
import javafx.application.Platform;
import javafx.beans.property.ReadOnlyObjectWrapper;
import javafx.beans.property.ReadOnlyStringWrapper;
import javafx.collections.FXCollections;
import javafx.geometry.Insets;
import javafx.geometry.Orientation;
import javafx.geometry.Pos;
import javafx.scene.AccessibleRole;
import javafx.scene.Node;
import javafx.scene.Parent;
import javafx.scene.control.Button;
import javafx.scene.control.ComboBox;
import javafx.scene.control.Label;
import javafx.scene.control.PasswordField;
import javafx.scene.control.ScrollPane;
import javafx.scene.control.Separator;
import javafx.scene.control.TableColumn;
import javafx.scene.control.TableView;
import javafx.scene.control.TextField;
import javafx.scene.control.Tooltip;
import javafx.scene.layout.BorderPane;
import javafx.scene.layout.FlowPane;
import javafx.scene.layout.GridPane;
import javafx.scene.layout.HBox;
import javafx.scene.layout.Priority;
import javafx.scene.layout.Region;
import javafx.scene.layout.StackPane;
import javafx.scene.layout.VBox;

import java.util.ArrayList;
import java.util.EnumMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/** One responsive JavaFX view hierarchy shared by Gluon Android and GNU/Linux. */
public final class MainView implements AutoCloseable {
    private static final double COMPACT_WIDTH = 760;

    private final Backend backend;
    private final DashboardLoader loader;
    private final BorderPane root = new BorderPane();
    private final StackPane content = new StackPane();
    private final VBox sideNavigation = new VBox(6);
    private final HBox bottomNavigation = new HBox(4);
    private final Map<Page, Node> pages = new EnumMap<>(Page.class);
    private final Map<Page, List<Button>> navigationButtons = new EnumMap<>(Page.class);
    private final ScheduledExecutorService refreshScheduler;
    private final AtomicBoolean refreshInFlight = new AtomicBoolean();

    private final Label pageTitle = new Label();
    private final Label connectionState = new Label("Checking…");
    private final Label errorBanner = new Label();
    private final ComboBox<String> profilePicker = new ComboBox<>();
    private final Button connectButton = new Button("Connect");
    private final Button refreshButton = new Button("Refresh");

    private final Label activeConnections = metricValue();
    private final Label downloadRate = metricValue();
    private final Label uploadRate = metricValue();
    private final Label totalTraffic = metricValue();
    private final Label listenerSummary = new Label("No active listeners");
    private final Label dnsSummary = new Label("DNS status unavailable");
    private final TableView<DashboardData.Connection> connectionsTable = new TableView<>();
    private final TableView<DashboardData.Rule> rulesTable = new TableView<>();
    private final TableView<DashboardData.Server> serversTable = new TableView<>();
    private final TableView<DashboardData.Capture> capturesTable = new TableView<>();
    private final Label captureSummary = new Label("Capture status unavailable");
    private final Button captureToggle = new Button("Enable capture");
    private final Label lastUpdated = new Label("Not refreshed yet");

    private volatile DashboardData currentData = DashboardData.empty();
    private volatile boolean updatingProfile;
    private Page currentPage = Page.DASHBOARD;

    public MainView(Backend backend) {
        this.backend = backend;
        this.loader = new DashboardLoader(backend);
        this.refreshScheduler = Executors.newSingleThreadScheduledExecutor(runnable -> {
            Thread thread = new Thread(runnable, "clambhook-refresh");
            thread.setDaemon(true);
            return thread;
        });
        configureRoot();
        configureTables();
        createPages();
        createNavigation();
        configureActions();
        showPage(Page.DASHBOARD);
        refresh();
        refreshScheduler.scheduleWithFixedDelay(this::refresh, 3, 3, TimeUnit.SECONDS);
    }

    public Parent node() {
        return root;
    }

    private void configureRoot() {
        root.getStyleClass().add("app-root");
        root.setTop(createHeader());
        root.setCenter(content);
        root.setLeft(sideNavigation);
        root.widthProperty().addListener((ignored, oldWidth, newWidth) ->
                updateResponsiveNavigation(newWidth.doubleValue()));
        Platform.runLater(() -> updateResponsiveNavigation(root.getWidth()));
    }

    private Node createHeader() {
        pageTitle.getStyleClass().add("page-title");
        connectionState.getStyleClass().addAll("status-pill", "status-pending");
        connectionState.setAccessibleRole(AccessibleRole.TEXT);
        errorBanner.getStyleClass().add("error-banner");
        errorBanner.setWrapText(true);
        errorBanner.setManaged(false);
        errorBanner.setVisible(false);

        profilePicker.setPromptText("Profile");
        profilePicker.setAccessibleText("Active profile");
        profilePicker.setPrefWidth(190);
        refreshButton.setTooltip(new Tooltip("Refresh all ClambHook data"));
        connectButton.getStyleClass().add("primary-button");

        Region spacer = new Region();
        HBox.setHgrow(spacer, Priority.ALWAYS);
        HBox toolbar = new HBox(10, pageTitle, connectionState, spacer,
                profilePicker, refreshButton, connectButton);
        toolbar.setAlignment(Pos.CENTER_LEFT);
        toolbar.getStyleClass().add("toolbar");

        VBox header = new VBox(errorBanner, toolbar);
        header.getStyleClass().add("header");
        return header;
    }

    private void createPages() {
        pages.put(Page.DASHBOARD, createDashboardPage());
        pages.put(Page.CONNECTIONS, tablePage(
                "Live and recent routed connections", connectionsTable));
        pages.put(Page.RULES, tablePage(
                "Rules are evaluated in profile order. Mutations remain available through the native API.", rulesTable));
        pages.put(Page.SERVERS, tablePage(
                "Configured chains and their ordered protocol hops", serversTable));
        pages.put(Page.DEVELOPER, createDeveloperPage());
        pages.put(Page.SETTINGS, createSettingsPage());
        content.getChildren().addAll(pages.values());
    }

    private Node createDashboardPage() {
        FlowPane metrics = new FlowPane(12, 12,
                metricCard("Active connections", activeConnections),
                metricCard("Download", downloadRate),
                metricCard("Upload", uploadRate),
                metricCard("Transferred", totalTraffic));
        metrics.setPrefWrapLength(900);

        Label listenersTitle = sectionTitle("Listeners");
        listenerSummary.setWrapText(true);
        listenerSummary.getStyleClass().add("secondary-text");
        VBox listenersCard = card(listenersTitle, listenerSummary);

        Label dnsTitle = sectionTitle("Encrypted DNS");
        dnsSummary.setWrapText(true);
        dnsSummary.getStyleClass().add("secondary-text");
        VBox dnsCard = card(dnsTitle, dnsSummary);

        lastUpdated.getStyleClass().add("secondary-text");
        VBox body = new VBox(18, metrics, listenersCard, dnsCard, lastUpdated);
        body.getStyleClass().add("page-content");
        return scroll(body);
    }

    private Node createDeveloperPage() {
        captureSummary.getStyleClass().add("secondary-text");
        Region spacer = new Region();
        HBox.setHgrow(spacer, Priority.ALWAYS);
        HBox controls = new HBox(10, captureSummary, spacer, captureToggle);
        controls.setAlignment(Pos.CENTER_LEFT);
        VBox body = new VBox(12, controls, capturesTable);
        body.getStyleClass().add("page-content");
        VBox.setVgrow(capturesTable, Priority.ALWAYS);
        return body;
    }

    private Node createSettingsPage() {
        VBox body = new VBox(18);
        body.getStyleClass().add("page-content");
        body.getChildren().addAll(
                sectionTitle("Runtime adapter"),
                detailRow("Platform", backend.displayName()),
                detailRow("UI toolkit", "JavaFX 21, deployed by GluonFX"),
                detailRow("Android support", "Android 12 / API 31 and newer"));

        if (backend instanceof HttpBackend httpBackend) {
            TextField baseUrl = new TextField(httpBackend.baseUrl());
            baseUrl.setPromptText("http://127.0.0.1:9090");
            baseUrl.setAccessibleText("ClambHook API URL");
            PasswordField token = new PasswordField();
            token.setPromptText("Optional bearer token");
            token.setAccessibleText("ClambHook API bearer token");
            Button apply = new Button("Apply connection settings");
            apply.getStyleClass().add("primary-button");
            apply.setOnAction(ignored -> {
                try {
                    httpBackend.configure(baseUrl.getText(), token.getText());
                    clearError();
                    refresh();
                } catch (RuntimeException error) {
                    showError(error);
                }
            });
            GridPane form = new GridPane();
            form.setHgap(12);
            form.setVgap(12);
            form.add(new Label("API URL"), 0, 0);
            form.add(baseUrl, 1, 0);
            form.add(new Label("Bearer token"), 0, 1);
            form.add(token, 1, 1);
            GridPane.setHgrow(baseUrl, Priority.ALWAYS);
            GridPane.setHgrow(token, Priority.ALWAYS);
            body.getChildren().addAll(new Separator(), sectionTitle("GNU/Linux daemon"), form, apply);
        }
        body.getChildren().addAll(
                new Separator(),
                sectionTitle("Licensing"),
                new Label("ClambHook remains under its repository license. JavaFX and Gluon notices are packaged separately."));
        return scroll(body);
    }

    private Node tablePage(String description, TableView<?> table) {
        Label label = new Label(description);
        label.setWrapText(true);
        label.getStyleClass().add("secondary-text");
        VBox body = new VBox(12, label, table);
        body.getStyleClass().add("page-content");
        VBox.setVgrow(table, Priority.ALWAYS);
        return body;
    }

    private void configureTables() {
        configureConnectionTable();
        configureRuleTable();
        configureServerTable();
        configureCaptureTable();
        for (TableView<?> table : List.of(connectionsTable, rulesTable, serversTable, capturesTable)) {
            table.setColumnResizePolicy(TableView.CONSTRAINED_RESIZE_POLICY_FLEX_LAST_COLUMN);
            table.setPlaceholder(new Label("No data"));
            table.getStyleClass().add("data-table");
        }
    }

    private void configureConnectionTable() {
        connectionsTable.getColumns().add(textColumn("Target", 260,
                row -> row.getValue().target()));
        connectionsTable.getColumns().add(textColumn("Network", 90,
                row -> row.getValue().network().toUpperCase()));
        connectionsTable.getColumns().add(textColumn("Route", 140,
                row -> displayRoute(row.getValue())));
        connectionsTable.getColumns().add(textColumn("State", 100,
                row -> row.getValue().state()));
        connectionsTable.getColumns().add(textColumn("Application", 150,
                row -> row.getValue().application()));
        connectionsTable.getColumns().add(textColumn("Traffic", 160,
                row -> Formatters.rate(row.getValue().downloadBytesPerSecond()) + " ↓  "
                        + Formatters.rate(row.getValue().uploadBytesPerSecond()) + " ↑"));
    }

    private void configureRuleTable() {
        rulesTable.getColumns().add(textColumn("Name", 180, row -> row.getValue().name()));
        rulesTable.getColumns().add(textColumn("Action", 120, row -> row.getValue().action()));
        rulesTable.getColumns().add(textColumn("Matches", 480, row -> row.getValue().matchSummary()));
    }

    private void configureServerTable() {
        serversTable.getColumns().add(textColumn("Chain", 150, row -> row.getValue().chain()));
        serversTable.getColumns().add(textColumn("Server", 170, row -> row.getValue().name()));
        serversTable.getColumns().add(textColumn("Protocol", 120, row -> row.getValue().protocol()));
        serversTable.getColumns().add(textColumn("Address", 230, row -> row.getValue().address()));
        serversTable.getColumns().add(textColumn("Location", 180, row -> row.getValue().location()));
    }

    private void configureCaptureTable() {
        capturesTable.getColumns().add(textColumn("Method", 90, row -> row.getValue().method()));
        capturesTable.getColumns().add(textColumn("URL", 430, row -> row.getValue().url()));
        TableColumn<DashboardData.Capture, Number> status = new TableColumn<>("Status");
        status.setPrefWidth(90);
        status.setCellValueFactory(row -> new ReadOnlyObjectWrapper<>(row.getValue().status()));
        capturesTable.getColumns().add(status);
        capturesTable.getColumns().add(textColumn("Error", 220, row -> row.getValue().error()));
    }

    private static <T> TableColumn<T, String> textColumn(
            String title, double width,
            java.util.function.Function<TableColumn.CellDataFeatures<T, String>, String> value) {
        TableColumn<T, String> column = new TableColumn<>(title);
        column.setPrefWidth(width);
        column.setCellValueFactory(row -> new ReadOnlyStringWrapper(value.apply(row)));
        return column;
    }

    private void createNavigation() {
        sideNavigation.getStyleClass().add("side-navigation");
        sideNavigation.setPadding(new Insets(16, 10, 16, 10));
        bottomNavigation.getStyleClass().add("bottom-navigation");
        bottomNavigation.setAlignment(Pos.CENTER);
        for (Page page : Page.values()) {
            Button side = navigationButton(page, false);
            Button bottom = navigationButton(page, true);
            sideNavigation.getChildren().add(side);
            bottomNavigation.getChildren().add(bottom);
            HBox.setHgrow(bottom, Priority.ALWAYS);
            navigationButtons.put(page, new ArrayList<>(List.of(side, bottom)));
        }
    }

    private Button navigationButton(Page page, boolean compact) {
        Button button = new Button(compact ? page.shortTitle : page.title);
        button.setMaxWidth(Double.MAX_VALUE);
        button.setAccessibleText("Open " + page.title);
        button.getStyleClass().add(compact ? "bottom-nav-button" : "side-nav-button");
        button.setOnAction(ignored -> showPage(page));
        return button;
    }

    private void configureActions() {
        refreshButton.setOnAction(ignored -> refresh());
        connectButton.setOnAction(ignored -> changeConnectionState());
        profilePicker.valueProperty().addListener((ignored, previous, selected) -> {
            if (!updatingProfile && selected != null && !selected.isBlank()
                    && !selected.equals(currentData.activeProfile())) {
                runMutation(backend.put("/api/v1/profiles/active",
                        Json.object(Map.of("name", selected))));
            }
        });
        captureToggle.setOnAction(ignored -> runMutation(
                backend.put("/api/v1/developer/settings",
                        Json.object(Map.of("enabled", !currentData.developer().enabled())))));
    }

    private void changeConnectionState() {
        if (!backend.supportsConnectionControl()) {
            return;
        }
        connectButton.setDisable(true);
        String path = currentData.running() ? "/api/v1/disconnect" : "/api/v1/connect";
        runMutation(backend.post(path, "{}"));
    }

    private void runMutation(CompletableFuture<String> operation) {
        operation.whenComplete((ignored, error) -> Platform.runLater(() -> {
            connectButton.setDisable(false);
            if (error != null) {
                showError(error);
            } else {
                clearError();
                refresh();
            }
        }));
    }

    private void refresh() {
        if (!refreshInFlight.compareAndSet(false, true)) {
            return;
        }
        loader.load().whenComplete((data, error) -> {
            refreshInFlight.set(false);
            Platform.runLater(() -> {
                if (error != null) {
                    showError(error);
                    connectionState.setText("Unavailable");
                    setStatusClass("status-error");
                    return;
                }
                currentData = data;
                clearError();
                applyData(data);
            });
        });
    }

    private void applyData(DashboardData data) {
        connectionState.setText(data.running() ? "Connected" : "Disconnected");
        setStatusClass(data.running() ? "status-connected" : "status-disconnected");
        connectButton.setText(data.running() ? "Disconnect" : "Connect");
        connectButton.setDisable(false);

        updatingProfile = true;
        profilePicker.setItems(FXCollections.observableArrayList(data.profiles()));
        profilePicker.setValue(data.activeProfile().isBlank() ? null : data.activeProfile());
        updatingProfile = false;

        activeConnections.setText(Long.toString(data.traffic().activeConnections()));
        downloadRate.setText(Formatters.rate(data.traffic().downloadBytesPerSecond()));
        uploadRate.setText(Formatters.rate(data.traffic().uploadBytesPerSecond()));
        totalTraffic.setText(Formatters.bytes(
                data.traffic().receivedBytes() + data.traffic().transmittedBytes()));

        if (data.listeners().isEmpty()) {
            listenerSummary.setText("No active listeners");
        } else {
            listenerSummary.setText(data.listeners().stream()
                    .map(listener -> listener.protocol().toUpperCase() + " " + listener.address()
                            + " · " + Formatters.count(listener.activeConnections(), "connection", "connections"))
                    .reduce((left, right) -> left + "\n" + right).orElse(""));
        }
        String dnsState = data.dns().enabled() ? "Enabled" : "Disabled";
        String upstreamText = data.dns().upstreams().isEmpty()
                ? "No encrypted upstreams" : String.join("\n", data.dns().upstreams());
        dnsSummary.setText(dnsState + " · " + data.dns().strategy() + "\n" + upstreamText);

        connectionsTable.setItems(FXCollections.observableArrayList(data.connections()));
        rulesTable.setItems(FXCollections.observableArrayList(data.rules()));
        serversTable.setItems(FXCollections.observableArrayList(data.servers()));
        capturesTable.setItems(FXCollections.observableArrayList(data.developer().captures()));
        captureSummary.setText((data.developer().enabled() ? "Capture enabled" : "Capture disabled")
                + " · " + Formatters.count(data.developer().captureCount(), "entry", "entries"));
        captureToggle.setText(data.developer().enabled() ? "Disable capture" : "Enable capture");
        lastUpdated.setText("Updated just now · " + backend.displayName());
    }

    private void showPage(Page page) {
        currentPage = page;
        pageTitle.setText(page.title);
        for (Map.Entry<Page, Node> entry : pages.entrySet()) {
            boolean visible = entry.getKey() == page;
            entry.getValue().setVisible(visible);
            entry.getValue().setManaged(visible);
        }
        for (Map.Entry<Page, List<Button>> entry : navigationButtons.entrySet()) {
            boolean selected = entry.getKey() == page;
            for (Button button : entry.getValue()) {
                button.getStyleClass().remove("nav-selected");
                if (selected) {
                    button.getStyleClass().add("nav-selected");
                }
            }
        }
    }

    private void updateResponsiveNavigation(double width) {
        boolean compact = width > 0 && width < COMPACT_WIDTH;
        root.setLeft(compact ? null : sideNavigation);
        root.setBottom(compact ? bottomNavigation : null);
        profilePicker.setVisible(!compact);
        profilePicker.setManaged(!compact);
        refreshButton.setText(compact ? "↻" : "Refresh");
    }

    private void showError(Throwable throwable) {
        Throwable error = unwrap(throwable);
        String message = error.getMessage();
        errorBanner.setText(message == null || message.isBlank() ? error.toString() : message);
        errorBanner.setManaged(true);
        errorBanner.setVisible(true);
    }

    private void clearError() {
        errorBanner.setText("");
        errorBanner.setManaged(false);
        errorBanner.setVisible(false);
    }

    private static Throwable unwrap(Throwable throwable) {
        Throwable current = throwable;
        while ((current instanceof CompletionException || current instanceof java.util.concurrent.ExecutionException)
                && current.getCause() != null) {
            current = current.getCause();
        }
        return current;
    }

    private void setStatusClass(String styleClass) {
        connectionState.getStyleClass().removeAll(
                "status-pending", "status-connected", "status-disconnected", "status-error");
        connectionState.getStyleClass().add(styleClass);
    }

    private static Label metricValue() {
        Label label = new Label("—");
        label.getStyleClass().add("metric-value");
        return label;
    }

    private static VBox metricCard(String title, Label value) {
        Label titleLabel = new Label(title);
        titleLabel.getStyleClass().add("metric-title");
        VBox card = new VBox(6, titleLabel, value);
        card.getStyleClass().addAll("card", "metric-card");
        return card;
    }

    private static VBox card(Node... children) {
        VBox card = new VBox(10, children);
        card.getStyleClass().add("card");
        return card;
    }

    private static Label sectionTitle(String text) {
        Label label = new Label(text);
        label.getStyleClass().add("section-title");
        return label;
    }

    private static Node detailRow(String label, String value) {
        Label name = new Label(label);
        name.getStyleClass().add("detail-label");
        Label content = new Label(value);
        content.setWrapText(true);
        HBox row = new HBox(12, name, content);
        row.setAlignment(Pos.BASELINE_LEFT);
        return row;
    }

    private static ScrollPane scroll(Node content) {
        ScrollPane scroll = new ScrollPane(content);
        scroll.setFitToWidth(true);
        scroll.setHbarPolicy(ScrollPane.ScrollBarPolicy.NEVER);
        scroll.getStyleClass().add("page-scroll");
        return scroll;
    }

    private static String displayRoute(DashboardData.Connection connection) {
        if (!connection.chain().isBlank()) {
            return connection.chain();
        }
        return connection.action().isBlank() ? "—" : connection.action();
    }

    @Override
    public void close() {
        refreshScheduler.shutdownNow();
    }

    private enum Page {
        DASHBOARD("Dashboard", "Home"),
        CONNECTIONS("Connections", "Traffic"),
        RULES("Rules", "Rules"),
        SERVERS("Servers", "Servers"),
        DEVELOPER("Developer", "Capture"),
        SETTINGS("Settings", "Settings");

        private final String title;
        private final String shortTitle;

        Page(String title, String shortTitle) {
            this.title = title;
            this.shortTitle = shortTitle;
        }
    }
}
