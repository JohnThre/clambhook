// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui;

import com.clambhook.ui.format.Formatters;
import com.clambhook.ui.json.Json;
import com.clambhook.ui.model.DashboardData;
import com.clambhook.ui.model.DashboardLoader;
import com.clambhook.ui.platform.PlatformServices;
import com.clambhook.ui.runtime.RuntimeClient;
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
import javafx.scene.Scene;
import javafx.scene.control.Button;
import javafx.scene.control.ComboBox;
import javafx.scene.control.Label;
import javafx.scene.control.ListView;
import javafx.scene.control.PasswordField;
import javafx.scene.control.ScrollPane;
import javafx.scene.control.Separator;
import javafx.scene.control.Tab;
import javafx.scene.control.TabPane;
import javafx.scene.control.TableColumn;
import javafx.scene.control.TableView;
import javafx.scene.control.TextArea;
import javafx.scene.control.TextField;
import javafx.scene.control.Tooltip;
import javafx.scene.input.KeyCode;
import javafx.scene.input.KeyCodeCombination;
import javafx.scene.input.KeyCombination;
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
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.Executors;
import java.util.concurrent.Flow;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Function;
import java.util.function.Supplier;

/** One responsive JavaFX view hierarchy shared by Gluon Android and GNU/Linux. */
public final class MainView implements AutoCloseable {
    private static final double COMPACT_WIDTH = 760;
    private static final String CLAMBHOOK_BUY_URL = "https://store.swiphtgroup.com/clambhook/buy/";
    private static final String CLAMBHOOK_PORTAL_URL = "https://store.swiphtgroup.com/clambhook/portal/";
    private static final Map<String, String> DONATION_URLS = Map.of(
            "Ko-fi", "https://ko-fi.com/jpfchang",
            "Liberapay", "https://en.liberapay.com/jpfchang/",
            "IssueHunt", "https://oss.issuehunt.io/u/johnthre",
            "Donate crypto", "https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd");

    private final RuntimeClient runtime;
    private final PlatformServices platformServices;
    private final DashboardLoader loader;
    private final BorderPane root = new BorderPane();
    private final StackPane content = new StackPane();
    private final VBox sideNavigation = new VBox(6);
    private final HBox bottomNavigation = new HBox(4);
    private final ComboBox<Page> compactPagePicker = new ComboBox<>();
    private final Map<Page, Node> pages = new EnumMap<>(Page.class);
    private final Map<Page, List<Button>> navigationButtons = new EnumMap<>(Page.class);
    private final Map<Page, Runnable> pageRefreshers = new EnumMap<>(Page.class);
    private final ScheduledExecutorService refreshScheduler;
    private final AtomicBoolean refreshInFlight = new AtomicBoolean();
    private final AtomicBoolean outlineLinkReadInFlight = new AtomicBoolean();
    private final AtomicBoolean closed = new AtomicBoolean();
    private final AtomicReference<Flow.Subscription> liveEventSubscription =
            new AtomicReference<>();

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
    private final ListView<String> profilesList = new ListView<>();
    private final TextArea configTransfer = documentArea("TOML configuration");
    private final TextArea outlineKey = new TextArea();
    private final TextField outlineName = new TextField("Outline");
    private final TextArea decisionsDocument = documentArea("Traffic decisions JSON");
    private final TextArea temporaryRulesDocument = documentArea("Temporary rules JSON");
    private final TextArea policyGroupsDocument = documentArea("Policy groups JSON");
    private final TextArea rulesDocument = documentArea("Rules JSON");
    private final TextArea ruleSetsDocument = documentArea("Rule sets JSON");
    private final TextArea subscriptionsDocument = documentArea("Rule subscriptions JSON");
    private final TextArea dnsDocument = documentArea("DNS settings JSON");
    private final TextArea settingsDocument = documentArea("Runtime settings JSON");
    private final TextArea conditionerDocument = documentArea("Conditioner settings JSON");
    private final TextArea pendingPromptsDocument = documentArea("Pending prompts JSON");
    private final TextArea promptDecisionsDocument = documentArea("Prompt decisions JSON");
    private final TextArea developerSettingsDocument = documentArea("Developer settings JSON");
    private final TextArea developerMapRulesDocument = documentArea("Developer map rules JSON");
    private final TextArea developerBreakpointRulesDocument = documentArea("Developer breakpoint rules JSON");
    private final TextArea developerRewriteRulesDocument = documentArea("Developer rewrite rules JSON");
    private final TextArea developerPendingBreakpointsDocument = documentArea("Pending developer breakpoints JSON");
    private final TextArea developerCaDocument = documentArea("Developer certificate authority PEM");
    private final TextArea developerComposer = documentArea("Developer request JSON");
    private final TextArea developerResult = documentArea("Developer response JSON");
    private final TextArea licenseDocument = documentArea("License status JSON");
    private final TextArea updateDocument = documentArea("Update status JSON");
    private final Label captureSummary = new Label("Capture status unavailable");
    private final Button captureToggle = new Button("Enable capture");
    private final Label lastUpdated = new Label("Not refreshed yet");
    private final Label dashboardSupporterBadge = new Label("Supporter status unavailable");
    private final Label settingsSupporterBadge = new Label("Supporter status unavailable");
    private final Label supporterThanks = new Label();

    private volatile DashboardData currentData = DashboardData.empty();
    private volatile boolean updatingProfile;
    private Page currentPage = Page.DASHBOARD;

    public MainView(RuntimeClient runtime, PlatformServices platformServices) {
        this.runtime = runtime;
        this.platformServices = platformServices;
        this.loader = new DashboardLoader(runtime);
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
        startLiveEvents();
        refreshScheduler.scheduleWithFixedDelay(this::refresh, 3, 3, TimeUnit.SECONDS);
    }

    public Parent node() {
        return root;
    }

    /** Routes an external key into review; it never imports or connects. */
    public void showOutlineAccessKey(String accessKey) {
        String value = accessKey == null ? "" : accessKey.trim();
        if (!value.regionMatches(true, 0, "ss://", 0, 5) &&
                !value.regionMatches(true, 0, "ssconf://", 0, 9)) {
            showError(new IllegalArgumentException(
                    "External Outline links must use ss:// or ssconf://"));
            return;
        }
        outlineKey.setText(value);
        showPage(Page.PROFILES);
        outlineKey.requestFocus();
    }

    /** Installs platform-standard keyboard navigation without stealing focus. */
    public void installAccelerators(Scene scene) {
        scene.getAccelerators().put(
                new KeyCodeCombination(KeyCode.R, KeyCombination.SHORTCUT_DOWN),
                this::refresh);
        scene.getAccelerators().put(
                new KeyCodeCombination(KeyCode.F5), this::refresh);
        scene.getAccelerators().put(
                new KeyCodeCombination(KeyCode.ESCAPE), this::clearError);
        KeyCode[] pageKeys = {
                KeyCode.DIGIT1, KeyCode.DIGIT2, KeyCode.DIGIT3,
                KeyCode.DIGIT4, KeyCode.DIGIT5, KeyCode.DIGIT6,
                KeyCode.DIGIT7, KeyCode.DIGIT8, KeyCode.DIGIT9
        };
        Page[] pageValues = Page.values();
        for (int index = 0; index < pageValues.length; ++index) {
            Page page = pageValues[index];
            scene.getAccelerators().put(new KeyCodeCombination(
                    pageKeys[index], KeyCombination.SHORTCUT_DOWN),
                    () -> showPage(page));
        }
    }

    private void configureRoot() {
        root.getStyleClass().add("app-root");
        root.setAccessibleRole(AccessibleRole.PARENT);
        root.setAccessibleText("ClambHook network controller");
        root.setTop(createHeader());
        root.setCenter(content);
        root.setLeft(sideNavigation);
        root.widthProperty().addListener((ignored, oldWidth, newWidth) ->
                updateResponsiveNavigation(newWidth.doubleValue()));
        Platform.runLater(() -> updateResponsiveNavigation(root.getWidth()));
    }

    private Node createHeader() {
        pageTitle.getStyleClass().add("page-title");
        pageTitle.setAccessibleText("Current page");
        connectionState.getStyleClass().addAll("status-pill", "status-pending");
        connectionState.setAccessibleRole(AccessibleRole.TEXT);
        errorBanner.getStyleClass().add("error-banner");
        errorBanner.setAccessibleRole(AccessibleRole.TEXT);
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
        pages.put(Page.PROFILES, createProfilesPage());
        pages.put(Page.ACTIVITY, createActivityPage());
        pages.put(Page.ROUTING, createRoutingPage());
        pages.put(Page.RULES, createRulesPage());
        pages.put(Page.NETWORK, createNetworkPage());
        pages.put(Page.PROMPTS, createPromptsPage());
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
        dashboardSupporterBadge.setAccessibleText("ClambHook supporter status");
        dashboardSupporterBadge.getStyleClass().add("secondary-text");
        VBox body = new VBox(18, dashboardSupporterBadge, metrics, listenersCard, dnsCard, lastUpdated);
        body.getStyleClass().add("page-content");
        if (platformServices.supports(PlatformServices.Capability.LICENSING)) {
            pageRefreshers.put(Page.DASHBOARD, () -> loadLicenseStatus(
                    platformServices.licensing("status", "{}"), null));
        }
        return scroll(body);
    }

    private Node createProfilesPage() {
        profilesList.setAccessibleText("Configured profiles");
        profilesList.setPrefHeight(220);
        Button activate = new Button("Activate selected profile");
        activate.getStyleClass().add("primary-button");
        activate.setOnAction(ignored -> {
            String selected = profilesList.getSelectionModel().getSelectedItem();
            if (selected == null || selected.isBlank()) {
                showError(new IllegalArgumentException("Select a profile first"));
            } else {
                runMutation(runtime.setActiveProfile(selected));
            }
        });

        Button load = new Button("Load current config");
        load.setOnAction(ignored -> loadRaw(runtime.exportConfig(), configTransfer));
        Button apply = new Button("Import and apply");
        apply.getStyleClass().add("primary-button");
        apply.setOnAction(ignored -> runMutation(runtime.importConfig(configTransfer.getText())));
        Button paste = new Button("Paste");
        paste.setDisable(!platformServices.supports(PlatformServices.Capability.CLIPBOARD));
        paste.setOnAction(ignored -> loadRaw(platformServices.clipboardRead(), configTransfer));
        Button copy = new Button("Copy");
        copy.setDisable(!platformServices.supports(PlatformServices.Capability.CLIPBOARD));
        copy.setOnAction(ignored -> runMutation(
                platformServices.clipboardWrite(configTransfer.getText())));
        Button scan = new Button("Scan QR");
        scan.setDisable(!platformServices.supports(PlatformServices.Capability.QR_SCAN));
        scan.setOnAction(ignored -> loadRaw(platformServices.scanQrCode(), configTransfer));
        Button share = new Button("Share QR");
        share.setDisable(!platformServices.supports(PlatformServices.Capability.QR_SHARE));
        share.setOnAction(ignored -> runMutation(
                platformServices.shareQrCode(configTransfer.getText())));

        TextField filePath = new TextField();
        filePath.setPromptText("Config path inside platform storage");
        filePath.setAccessibleText("Configuration file path");
        Button readFile = new Button("Read file");
        Button writeFile = new Button("Write file");
        boolean files = platformServices.supports(PlatformServices.Capability.FILES);
        readFile.setDisable(!files);
        writeFile.setDisable(!files);
        readFile.setOnAction(ignored -> {
            try {
                loadRaw(platformServices.readTextFile(
                        java.nio.file.Path.of(filePath.getText()), 4 * 1024 * 1024),
                        configTransfer);
            } catch (RuntimeException error) {
                showError(error);
            }
        });
        writeFile.setOnAction(ignored -> {
            try {
                runMutation(platformServices.writeTextFile(
                        java.nio.file.Path.of(filePath.getText()), configTransfer.getText()));
            } catch (RuntimeException error) {
                showError(error);
            }
        });

        FlowPane transferActions = new FlowPane(8, 8,
                load, apply, paste, copy, scan, share);
        HBox fileActions = new HBox(8, filePath, readFile, writeFile);
        HBox.setHgrow(filePath, Priority.ALWAYS);
        VBox transfer = card(sectionTitle("Import, export, and QR"),
                new Label("TOML is validated transactionally. Exports can contain passwords and dynamic Outline URLs; handle them as secrets."),
                configTransfer, transferActions, fileActions);
        outlineKey.setPromptText("ss:// or ssconf:// access key");
        outlineKey.setAccessibleText("Outline access key");
        outlineKey.setPrefRowCount(3);
        outlineName.setPromptText("Profile name");
        outlineName.setAccessibleText("Outline profile name");
        Label outlineReview = new Label(
                "Review an access key before importing. Keys are never auto-connected.");
        outlineReview.setWrapText(true);
        outlineReview.getStyleClass().add("secondary-text");
        Button reviewOutline = new Button("Review access key");
        Button importOutline = new Button("Import Outline profile");
        importOutline.getStyleClass().add("primary-button");
        importOutline.setDisable(true);
        AtomicReference<String> reviewedKey = new AtomicReference<>("");
        AtomicReference<String> reviewedName = new AtomicReference<>("");
        Runnable invalidateOutlineReview = () -> {
            reviewedKey.set("");
            reviewedName.set("");
            importOutline.setDisable(true);
        };
        outlineKey.textProperty().addListener((ignored, oldValue, newValue) ->
                invalidateOutlineReview.run());
        outlineName.textProperty().addListener((ignored, oldValue, newValue) ->
                invalidateOutlineReview.run());
        Button pasteOutline = new Button("Paste key");
        pasteOutline.setDisable(!platformServices.supports(
                PlatformServices.Capability.CLIPBOARD));
        pasteOutline.setOnAction(ignored -> platformServices.clipboardRead()
                .whenComplete((value, failure) -> Platform.runLater(() -> {
                    if (failure != null) showError(failure);
                    else {
                        outlineKey.setText(value == null ? "" : value.trim());
                        importOutline.setDisable(true);
                    }
                })));
        Button scanOutline = new Button("Scan key QR");
        scanOutline.setDisable(!platformServices.supports(
                PlatformServices.Capability.QR_SCAN));
        scanOutline.setOnAction(ignored -> platformServices.scanQrCode()
                .whenComplete((value, failure) -> Platform.runLater(() -> {
                    if (failure != null) showError(failure);
                    else {
                        outlineKey.setText(value == null ? "" : value.trim());
                        importOutline.setDisable(true);
                    }
                })));
        reviewOutline.setOnAction(ignored -> {
            String requestedKey = outlineKey.getText();
            runtime.reviewOutlineAccessKey(requestedKey).whenComplete((preview, failure) ->
                Platform.runLater(() -> {
                    if (failure != null) {
                        invalidateOutlineReview.run();
                        showError(failure);
                        return;
                    }
                    if (!requestedKey.equals(outlineKey.getText())) {
                        invalidateOutlineReview.run();
                        return;
                    }
                    String suggested = preview.root().get("suggested_name").text();
                    if (!suggested.isBlank() && (outlineName.getText().isBlank() ||
                            "Outline".equals(outlineName.getText()))) {
                        outlineName.setText(suggested);
                    }
                    outlineReview.setText("Compatibility preview (credentials hidden):\n" +
                            preview.rawJson() + "\nWill create profile: " + outlineName.getText());
                    reviewedKey.set(requestedKey);
                    reviewedName.set(outlineName.getText());
                    importOutline.setDisable(false);
                }));
        });
        importOutline.setOnAction(ignored -> {
            if (!reviewedKey.get().equals(outlineKey.getText()) ||
                    !reviewedName.get().equals(outlineName.getText())) {
                invalidateOutlineReview.run();
                showError(new IllegalStateException("Review this access key and profile name again"));
                return;
            }
            runMutation(runtime.importOutlineAccessKey(outlineKey.getText(),
                    outlineName.getText(), false));
        });
        Button refreshOutline = new Button("Refresh selected dynamic profile");
        refreshOutline.setOnAction(ignored -> {
            String selected = profilesList.getSelectionModel().getSelectedItem();
            if (selected == null || selected.isBlank()) {
                showError(new IllegalArgumentException("Select a profile first"));
            } else {
                runMutation(runtime.refreshOutlineProfile(selected));
            }
        });
        FlowPane outlineActions = new FlowPane(8, 8, pasteOutline, scanOutline,
                reviewOutline, importOutline, refreshOutline);
        VBox outline = card(sectionTitle("Import Outline access key"),
                new Label("Paste, scan, or open a standard ss:// or basic dynamic ssconf:// key."),
                outlineKey, outlineName, outlineReview, outlineActions);
        VBox profileCard = card(sectionTitle("Profiles"), profilesList, activate);
        VBox body = new VBox(16, profileCard, outline, transfer);
        body.getStyleClass().add("page-content");
        pageRefreshers.put(Page.PROFILES, () -> loadRaw(runtime.exportConfig(), configTransfer));
        return scroll(body);
    }

    private Node createActivityPage() {
        Node decisions = documentEditor(
                "Decision history", decisionsDocument,
                () -> runtime.decisions(RuntimeClient.TrafficQuery.recent(500)),
                null);
        Node temporary = documentEditor(
                "Temporary rules", temporaryRulesDocument,
                () -> runtime.temporaryRules(""), null);
        TabPane tabs = tabs(
                tab("Connections", tablePage(
                        "Live and retained traffic with application attribution", connectionsTable)),
                tab("Decisions", decisions),
                tab("Temporary rules", temporary));

        TextField ruleName = new TextField("temporary-rule");
        ruleName.setAccessibleText("New rule name");
        ComboBox<String> action = new ComboBox<>(
                FXCollections.observableArrayList("direct", "block", "reject"));
        action.setValue("direct");
        action.setAccessibleText("New rule action");
        Button temporaryFromConnection = new Button("Create temporary rule");
        Button persistentFromConnection = new Button("Create persistent rule");
        temporaryFromConnection.setOnAction(ignored -> createConnectionRule(
                ruleName.getText(), action.getValue(), false));
        persistentFromConnection.setOnAction(ignored -> createConnectionRule(
                ruleName.getText(), action.getValue(), true));
        FlowPane actions = new FlowPane(8, 8, ruleName, action,
                temporaryFromConnection, persistentFromConnection);
        VBox body = new VBox(12, actions, tabs);
        body.getStyleClass().add("page-content");
        VBox.setVgrow(tabs, Priority.ALWAYS);
        pageRefreshers.put(Page.ACTIVITY, () -> {
            loadDocument(runtime.decisions(RuntimeClient.TrafficQuery.recent(500)),
                    decisionsDocument);
            loadDocument(runtime.temporaryRules(""), temporaryRulesDocument);
        });
        return body;
    }

    private Node createRoutingPage() {
        Button test = new Button("Run policy health tests");
        test.setOnAction(ignored -> runMutation(
                runtime.testPolicyGroups("{}"),
                () -> loadDocument(runtime.policyGroups(""), policyGroupsDocument)));
        Node policies = documentEditor(
                "Policy groups and selected chains", policyGroupsDocument,
                () -> runtime.policyGroups(""), runtime::replacePolicyGroups, test);
        TabPane tabs = tabs(
                tab("Servers and chains", tablePage(
                        "Ordered protocol hops, capabilities, and resolved locations",
                        serversTable)),
                tab("Policy groups", policies));
        pageRefreshers.put(Page.ROUTING,
                () -> loadDocument(runtime.policyGroups(""), policyGroupsDocument));
        return tabs;
    }

    private Node createRulesPage() {
        Button refreshSets = new Button("Refresh selected/all rule sets");
        refreshSets.setOnAction(ignored -> runMutation(
                runtime.refreshRuleSets("{}"),
                () -> loadDocument(runtime.ruleSets(""), ruleSetsDocument)));
        Button refreshSubscriptions = new Button("Refresh selected/all subscriptions");
        refreshSubscriptions.setOnAction(ignored -> runMutation(
                runtime.refreshRuleSubscriptions("{}"),
                () -> loadDocument(runtime.ruleSubscriptions(""),
                        subscriptionsDocument)));
        TabPane tabs = tabs(
                tab("Resolved rules", tablePage(
                        "Rules are evaluated in profile order", rulesTable)),
                tab("Rule editor", documentEditor(
                        "Persisted profile rules", rulesDocument,
                        () -> runtime.rules(""), runtime::replaceRules)),
                tab("Rule sets", documentEditor(
                        "Remote and local rule sets", ruleSetsDocument,
                        () -> runtime.ruleSets(""), runtime::replaceRuleSets,
                        refreshSets)),
                tab("Subscriptions", documentEditor(
                        "Rule subscriptions", subscriptionsDocument,
                        () -> runtime.ruleSubscriptions(""),
                        runtime::replaceRuleSubscriptions,
                        refreshSubscriptions)));
        pageRefreshers.put(Page.RULES, () -> {
            loadDocument(runtime.rules(""), rulesDocument);
            loadDocument(runtime.ruleSets(""), ruleSetsDocument);
            loadDocument(runtime.ruleSubscriptions(""), subscriptionsDocument);
        });
        return tabs;
    }

    private Node createNetworkPage() {
        TabPane tabs = tabs(
                tab("DNS and firewall", documentEditor(
                        "Encrypted DNS and leak-prevention settings", dnsDocument,
                        () -> runtime.dns(""), runtime::updateDns)),
                tab("Capture and runtime", documentEditor(
                        "Firewall, capture, route, and runtime settings", settingsDocument,
                        () -> runtime.settings(""), runtime::updateSettings)),
                tab("Conditioner", documentEditor(
                        "Latency, jitter, bandwidth, and loss simulation",
                        conditionerDocument,
                        () -> runtime.conditioner(""), runtime::updateConditioner)));
        pageRefreshers.put(Page.NETWORK, () -> {
            loadDocument(runtime.dns(""), dnsDocument);
            loadDocument(runtime.settings(""), settingsDocument);
            loadDocument(runtime.conditioner(""), conditionerDocument);
        });
        return tabs;
    }

    private Node createPromptsPage() {
        TextField identifier = new TextField();
        identifier.setPromptText("Prompt or decision ID");
        identifier.setAccessibleText("Prompt or decision identifier");
        Button allow = new Button("Allow once");
        Button block = new Button("Block once");
        Button promote = new Button("Promote forever");
        allow.setOnAction(ignored -> resolvePrompt(identifier.getText(), true));
        block.setOnAction(ignored -> resolvePrompt(identifier.getText(), false));
        promote.setOnAction(ignored -> runMutation(
                runtime.promotePromptDecision(identifier.getText(),
                        "{\"action\":\"block\",\"scope\":\"forever\"}"),
                this::refreshPromptDocuments));
        FlowPane actions = new FlowPane(8, 8, identifier, allow, block, promote);
        TabPane documents = tabs(
                tab("Pending", documentEditor(
                        "Pending connection prompts", pendingPromptsDocument,
                        runtime::pendingPrompts, null)),
                tab("Silent decisions", documentEditor(
                        "Decisions eligible for persistent promotion",
                        promptDecisionsDocument, runtime::promptDecisions, null)));
        VBox body = new VBox(12, actions, documents);
        body.getStyleClass().add("page-content");
        VBox.setVgrow(documents, Priority.ALWAYS);
        pageRefreshers.put(Page.PROMPTS, this::refreshPromptDocuments);
        return body;
    }

    private Node createDeveloperPage() {
        captureSummary.getStyleClass().add("secondary-text");
        Region spacer = new Region();
        HBox.setHgrow(spacer, Priority.ALWAYS);
        Button clear = new Button("Clear entries");
        clear.setOnAction(ignored -> runMutation(runtime.clearDeveloperEntries()));
        HBox controls = new HBox(10, captureSummary, spacer, clear, captureToggle);
        controls.setAlignment(Pos.CENTER_LEFT);
        VBox captures = new VBox(12, controls, capturesTable);
        VBox.setVgrow(capturesTable, Priority.ALWAYS);

        Node settings = documentEditor(
                "Capture, redaction, TLS decryption, and cache settings",
                developerSettingsDocument, runtime::developerSettings,
                runtime::updateDeveloperSettings);
        Node mapRules = documentEditor(
                "Map Local and Map Remote rules are evaluated in order",
                developerMapRulesDocument, () -> runtime.developerMapRules(""),
                runtime::replaceDeveloperMapRules);
        Node breakpointRules = documentEditor(
                "Interactive request and response breakpoint rules",
                developerBreakpointRulesDocument,
                () -> runtime.developerBreakpointRules(""),
                runtime::replaceDeveloperBreakpointRules);
        Node rewriteRules = documentEditor(
                "Ordered request and response header, body, URL, and status rewrites",
                developerRewriteRulesDocument,
                () -> runtime.developerRewriteRules(""),
                runtime::replaceDeveloperRewriteRules);

        TextField breakpointId = new TextField();
        breakpointId.setPromptText("Breakpoint ID");
        breakpointId.setAccessibleText("Pending developer breakpoint identifier");
        Button continueBreakpoint = new Button("Continue");
        Button dropBreakpoint = new Button("Drop");
        continueBreakpoint.setOnAction(ignored -> resolveDeveloperBreakpoint(
                breakpointId.getText(), "continue"));
        dropBreakpoint.setOnAction(ignored -> resolveDeveloperBreakpoint(
                breakpointId.getText(), "drop"));
        Node pendingBreakpoints = documentEditor(
                "Paused requests continue automatically after 30 seconds",
                developerPendingBreakpointsDocument,
                runtime::developerPendingBreakpoints, null,
                breakpointId, continueBreakpoint, dropBreakpoint);

        developerCaDocument.setEditable(false);
        Button loadCa = new Button("Load CA PEM");
        loadCa.setOnAction(ignored -> loadRaw(runtime.developerCaPem(),
                developerCaDocument));
        Button copyCa = new Button("Copy CA PEM");
        copyCa.setDisable(!platformServices.supports(
                PlatformServices.Capability.CLIPBOARD));
        copyCa.setOnAction(ignored -> runMutation(
                platformServices.clipboardWrite(developerCaDocument.getText())));
        Button regenerateCa = new Button("Regenerate CA");
        regenerateCa.setOnAction(ignored -> runMutation(
                runtime.regenerateDeveloperCa(),
                () -> loadRaw(runtime.developerCaPem(), developerCaDocument)));
        VBox certificateAuthority = new VBox(10,
                new Label("Install this CA only on devices you control. Regeneration invalidates the previous certificate."),
                new FlowPane(8, 8, loadCa, copyCa, regenerateCa),
                developerCaDocument);
        certificateAuthority.getStyleClass().add("page-content");
        developerComposer.setText(
                "{\n  \"method\": \"GET\",\n  \"url\": \"https://example.com/\"\n}");
        developerResult.setEditable(false);
        Button importCurl = new Button("Import cURL JSON");
        importCurl.setOnAction(ignored -> loadDocument(
                runtime.importCurl(Json.object(Map.of("curl", developerComposer.getText()))),
                developerResult));
        Button send = new Button("Send request");
        send.getStyleClass().add("primary-button");
        send.setOnAction(ignored -> loadDocument(
                runtime.sendDeveloperRequest(developerComposer.getText()),
                developerResult));
        VBox composer = new VBox(10,
                new Label("Compose or import an HTTP request. Private targets remain blocked."),
                developerComposer, new HBox(8, importCurl, send), developerResult);
        composer.getStyleClass().add("page-content");

        TabPane tabs = tabs(
                tab("Captured traffic", captures),
                tab("Settings", settings),
                tab("Map", mapRules),
                tab("Breakpoints", breakpointRules),
                tab("Pending", pendingBreakpoints),
                tab("Rewrites", rewriteRules),
                tab("CA", certificateAuthority),
                tab("Composer", composer));
        VBox body = new VBox(12, tabs);
        body.getStyleClass().add("page-content");
        VBox.setVgrow(tabs, Priority.ALWAYS);
        pageRefreshers.put(Page.DEVELOPER, this::refreshDeveloperDocuments);
        return body;
    }

    private void refreshDeveloperDocuments() {
        loadDocument(runtime.developerSettings(), developerSettingsDocument);
        loadDocument(runtime.developerMapRules(""), developerMapRulesDocument);
        loadDocument(runtime.developerBreakpointRules(""),
                developerBreakpointRulesDocument);
        loadDocument(runtime.developerRewriteRules(""),
                developerRewriteRulesDocument);
        loadDocument(runtime.developerPendingBreakpoints(),
                developerPendingBreakpointsDocument);
    }

    private void resolveDeveloperBreakpoint(String identifier, String action) {
        if (identifier == null || identifier.isBlank()) {
            showError(new IllegalArgumentException("Enter a breakpoint identifier"));
            return;
        }
        runMutation(runtime.resolveDeveloperBreakpoint(identifier,
                Json.object(Map.of("action", action))),
                () -> loadDocument(runtime.developerPendingBreakpoints(),
                        developerPendingBreakpointsDocument));
    }

    private Node createSettingsPage() {
        VBox body = new VBox(18);
        body.getStyleClass().add("page-content");
        body.getChildren().addAll(
                sectionTitle("Runtime adapter"),
                detailRow("Platform", runtime.displayName()),
                detailRow("UI toolkit", "JavaFX 21, deployed by GluonFX"),
                detailRow("Android support", "Android 12 / API 31 and newer"));

        runtime.endpointSettings().ifPresent(endpoint -> {
            TextField baseUrl = new TextField(endpoint.baseUrl());
            baseUrl.setPromptText("http://127.0.0.1:9090");
            baseUrl.setAccessibleText("ClambHook API URL");
            PasswordField token = new PasswordField();
            token.setPromptText("Optional bearer token");
            token.setAccessibleText("ClambHook API bearer token");
            Button apply = new Button("Apply connection settings");
            apply.getStyleClass().add("primary-button");
            apply.setOnAction(ignored -> {
                try {
                    runtime.configureEndpoint(new RuntimeClient.EndpointSettings(
                            baseUrl.getText(), token.getText()));
                    clearError();
                    restartLiveEvents();
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
        });
        body.getChildren().addAll(new Separator(), createPerAppRoutingCard());

        TextField email = new TextField();
        email.setPromptText("License email");
        email.setAccessibleText("License email");
        PasswordField licenseKey = new PasswordField();
        licenseKey.setPromptText("License key");
        licenseKey.setAccessibleText("License key");
        Button licenseStatus = new Button("Refresh license");
        Button activate = new Button("Activate");
        Button deactivate = new Button("Deactivate this device");
        licenseStatus.setOnAction(ignored -> loadLicenseStatus(
                platformServices.licensing("status", "{}"), licenseDocument));
        activate.setOnAction(ignored -> loadLicenseStatus(
                platformServices.licensing("activate", Json.object(Map.of(
                        "email", email.getText(), "license_key", licenseKey.getText()))),
                licenseDocument));
        deactivate.setOnAction(ignored -> loadLicenseStatus(
                platformServices.licensing("deactivate", "{}"), licenseDocument));
        boolean licensing = platformServices.supports(PlatformServices.Capability.LICENSING);
        licenseStatus.setDisable(!licensing);
        activate.setDisable(!licensing);
        deactivate.setDisable(!licensing);
        Button buySubscription = externalLinkButton("Buy Subscription", CLAMBHOOK_BUY_URL);
        Button manageSubscription = externalLinkButton("Manage Subscription", CLAMBHOOK_PORTAL_URL);
        supporterThanks.setWrapText(true);
        supporterThanks.setAccessibleText("Supporter thank-you message");
        settingsSupporterBadge.setAccessibleText("ClambHook supporter status");
        settingsSupporterBadge.getStyleClass().add("secondary-text");
        FlowPane donationButtons = new FlowPane(8, 8);
        DONATION_URLS.forEach((label, url) -> donationButtons.getChildren().add(externalLinkButton(label, url)));
        Label donationNotice = new Label(
                "Donations never create licenses, extend subscriptions, change badges, or affect support priority.");
        donationNotice.setWrapText(true);
        donationNotice.getStyleClass().add("secondary-text");
        VBox licenseCard = card(sectionTitle("Licensing"),
                settingsSupporterBadge,
                supporterThanks,
                new HBox(8, email, licenseKey),
                new FlowPane(8, 8, licenseStatus, activate, deactivate),
                new FlowPane(8, 8, buySubscription, manageSubscription),
                sectionTitle("Support independent ClambHook development"),
                donationNotice,
                donationButtons,
                licenseDocument);
        HBox.setHgrow(email, Priority.ALWAYS);
        HBox.setHgrow(licenseKey, Priority.ALWAYS);

        Button checkUpdate = new Button("Check for updates");
        Button installUpdate = new Button("Install verified update");
        checkUpdate.setOnAction(ignored -> loadPlatformResult(
                platformServices.updates("check", "{}"), updateDocument));
        installUpdate.setOnAction(ignored -> loadPlatformResult(
                platformServices.updates("install", "{}"), updateDocument));
        boolean updates = platformServices.supports(PlatformServices.Capability.UPDATES);
        checkUpdate.setDisable(!updates);
        installUpdate.setDisable(!updates);
        VBox updateCard = card(sectionTitle("Updates"),
                new Label("Signed Android updates or the GNU/Linux package repository remain authoritative."),
                new HBox(8, checkUpdate, installUpdate), updateDocument);
        body.getChildren().addAll(new Separator(), licenseCard, updateCard,
                detailRow("Notices", "JavaFX, Gluon, native dependencies, and license texts are packaged with the application."));
        pageRefreshers.put(Page.SETTINGS, () -> {
            if (licensing) {
                loadLicenseStatus(platformServices.licensing("status", "{}"),
                        licenseDocument);
            }
            if (updates) {
                loadPlatformResult(platformServices.updates("check", "{}"),
                        updateDocument);
            }
        });
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
        connectionsTable.setAccessibleText("Network connections");
        rulesTable.setAccessibleText("Routing rules");
        serversTable.setAccessibleText("Servers and chains");
        capturesTable.setAccessibleText("Developer traffic captures");
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
        compactPagePicker.setItems(FXCollections.observableArrayList(Page.values()));
        compactPagePicker.setAccessibleText("Current section");
        compactPagePicker.setMaxWidth(Double.MAX_VALUE);
        compactPagePicker.valueProperty().addListener((ignored, previous, selected) -> {
            if (selected != null && selected != currentPage) showPage(selected);
        });
        bottomNavigation.getChildren().add(compactPagePicker);
        HBox.setHgrow(compactPagePicker, Priority.ALWAYS);
        for (Page page : Page.values()) {
            Button side = navigationButton(page, false);
            sideNavigation.getChildren().add(side);
            navigationButtons.put(page, new ArrayList<>(List.of(side)));
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
                runMutation(runtime.setActiveProfile(selected));
            }
        });
        captureToggle.setOnAction(ignored -> runMutation(
                runtime.request("PUT", "/api/v1/developer/settings",
                        Json.object(Map.of("enabled", !currentData.developer().enabled())))));
    }

    private void changeConnectionState() {
        if (!runtime.supportsConnectionControl()) {
            return;
        }
        connectButton.setDisable(true);
        CompletableFuture<?> operation;
        if (currentData.running()) {
            operation = platformServices.stopVpn();
        } else {
            operation = platformServices.requestVpnConsent().thenCompose(granted -> {
                if (!granted) {
                    return CompletableFuture.failedFuture(
                            new IllegalStateException("VPN consent was not granted"));
                }
                return platformServices.startVpn();
            });
        }
        runMutation(operation);
    }

    private void runMutation(CompletableFuture<?> operation) {
        runMutation(operation, this::refresh);
    }

    private void runMutation(CompletableFuture<?> operation, Runnable afterSuccess) {
        operation.whenComplete((ignored, error) -> Platform.runLater(() -> {
            connectButton.setDisable(false);
            if (error != null) {
                showError(error);
            } else {
                clearError();
                if (afterSuccess != null) afterSuccess.run();
            }
        }));
    }

    private void loadDocument(CompletableFuture<RuntimeClient.Document> operation,
                              TextArea destination) {
        operation.whenComplete((document, error) -> Platform.runLater(() -> {
            if (error != null) {
                showError(error);
            } else {
                clearError();
                destination.setText(document.rawJson());
                destination.positionCaret(0);
            }
        }));
    }

    private void loadRaw(CompletableFuture<String> operation, TextArea destination) {
        operation.whenComplete((value, error) -> Platform.runLater(() -> {
            if (error != null) {
                showError(error);
            } else {
                clearError();
                destination.setText(value == null ? "" : value);
                destination.positionCaret(0);
            }
        }));
    }

    private void loadPlatformResult(
            CompletableFuture<PlatformServices.Result> operation,
            TextArea destination) {
        operation.whenComplete((result, error) -> Platform.runLater(() -> {
            if (error != null) {
                showError(error);
                return;
            }
            destination.setText(result.payload().isBlank()
                    ? result.message() : result.payload());
            destination.positionCaret(0);
            if (result.successful()) clearError();
            else showError(new IllegalStateException(result.message()));
        }));
    }

    private void loadLicenseStatus(
            CompletableFuture<PlatformServices.Result> operation,
            TextArea destination) {
        operation.whenComplete((result, error) -> Platform.runLater(() -> {
            if (error != null) {
                showError(error);
                return;
            }
            if (destination != null) {
                destination.setText(result.payload().isBlank() ? result.message() : result.payload());
                destination.positionCaret(0);
            }
            try {
                Json.Node decision = Json.parse(result.payload()).get("status").get("decision");
                String tier = decision.get("supporterTier").text("none");
                boolean active = decision.get("supporterActive").bool(false);
                if ("none".equals(tier)) {
                    dashboardSupporterBadge.setText("No verified supporter entitlement");
                    settingsSupporterBadge.setText("No verified supporter entitlement");
                    supporterThanks.setText("");
                } else {
                    String title = Character.toUpperCase(tier.charAt(0)) + tier.substring(1) + " Supporter";
                    String badge = title + (active ? " · Active" : " · Perpetual fallback");
                    dashboardSupporterBadge.setText(badge);
                    settingsSupporterBadge.setText(badge);
                    supporterThanks.setText("Thank you for supporting independent ClambHook development. " +
                            (active ? "Your paid-through period is current." :
                                    "Your compatible fallback and supporter badge remain yours."));
                }
            } catch (RuntimeException parseError) {
                dashboardSupporterBadge.setText("Supporter status unavailable");
                settingsSupporterBadge.setText("Supporter status unavailable");
            }
            if (result.successful()) clearError();
            else showError(new IllegalStateException(result.message()));
        }));
    }

    private Button externalLinkButton(String label, String uri) {
        Button button = new Button(label);
        button.setAccessibleText(label + " (opens in browser)");
        button.setDisable(!platformServices.supports(PlatformServices.Capability.BROWSER));
        button.setOnAction(ignored -> runMutation(platformServices.openBrowser(uri)));
        return button;
    }

    private void createConnectionRule(String name, String action,
                                      boolean persistent) {
        DashboardData.Connection selected =
                connectionsTable.getSelectionModel().getSelectedItem();
        if (selected == null) {
            showError(new IllegalArgumentException("Select a connection first"));
            return;
        }
        String request = Json.object(Map.of(
                "conn_id", selected.id(),
                "profile", currentData.activeProfile(),
                "name", name == null ? "" : name,
                "action", action == null ? "direct" : action,
                "scope", "auto",
                "ttl_seconds", 900));
        CompletableFuture<?> operation = persistent
                ? runtime.createRuleFromConnection(request)
                : runtime.createTemporaryRuleFromConnection(request);
        runMutation(operation, () -> {
            refresh();
            Runnable pageRefresh = pageRefreshers.get(Page.ACTIVITY);
            if (pageRefresh != null) pageRefresh.run();
        });
    }

    private void resolvePrompt(String identifier, boolean allow) {
        if (identifier == null || identifier.isBlank()) {
            showError(new IllegalArgumentException("Enter a prompt identifier"));
            return;
        }
        runMutation(runtime.resolvePrompt(identifier, Json.object(Map.of(
                "action", allow ? "allow" : "block", "scope", "once"))),
                this::refreshPromptDocuments);
    }

    private void refreshPromptDocuments() {
        loadDocument(runtime.pendingPrompts(), pendingPromptsDocument);
        loadDocument(runtime.promptDecisions(), promptDecisionsDocument);
    }

    private Node createPerAppRoutingCard() {
        ComboBox<String> mode = new ComboBox<>(
                FXCollections.observableArrayList("all", "include", "exclude"));
        mode.setValue("all");
        mode.setAccessibleText("Per-application routing mode");
        TextArea packages = documentArea("One Android package name per line");
        packages.setPrefRowCount(7);
        Button load = new Button("Load routing settings");
        Button inventory = new Button("List installed apps");
        Button apply = new Button("Apply per-app routing");
        apply.getStyleClass().add("primary-button");
        boolean supported = platformServices.supports(
                PlatformServices.Capability.PER_APP_ROUTING);
        load.setDisable(!supported);
        inventory.setDisable(!supported);
        apply.setDisable(!supported);
        load.setOnAction(ignored -> platformServices.appRoutingSettings()
                .whenComplete((settings, error) -> Platform.runLater(() -> {
                    if (error != null) {
                        showError(error);
                    } else {
                        mode.setValue(settings.mode());
                        packages.setText(String.join("\n", settings.packageNames()));
                        clearError();
                    }
                })));
        inventory.setOnAction(ignored -> platformServices.installedApplications()
                .whenComplete((applications, error) -> Platform.runLater(() -> {
                    if (error != null) {
                        showError(error);
                    } else {
                        packages.setText(applications.stream()
                                .map(application -> application.packageName() +
                                        " # " + application.label())
                                .reduce((left, right) -> left + "\n" + right)
                                .orElse(""));
                        clearError();
                    }
                })));
        apply.setOnAction(ignored -> {
            Set<String> selected = packages.getText().lines()
                    .map(line -> line.replaceFirst("\\s+#.*$", "").trim())
                    .filter(line -> !line.isBlank())
                    .collect(java.util.stream.Collectors.toCollection(
                            java.util.LinkedHashSet::new));
            runMutation(platformServices.updateAppRoutingSettings(
                    mode.getValue(), selected));
        });
        return card(sectionTitle("Per-application routing"),
                new Label(supported
                        ? "Android VPN inclusion and exclusion lists are owned by the Kotlin service layer."
                        : "Per-application routing is only available on Android."),
                new HBox(8, new Label("Mode"), mode), packages,
                new FlowPane(8, 8, load, inventory, apply));
    }

    private Node documentEditor(
            String description,
            TextArea area,
            Supplier<CompletableFuture<RuntimeClient.Document>> loader,
            Function<String, CompletableFuture<?>> saver,
            Node... additionalActions) {
        Label help = new Label(description);
        help.setWrapText(true);
        help.getStyleClass().add("secondary-text");
        Button reload = new Button("Reload");
        reload.setOnAction(ignored -> loadDocument(loader.get(), area));
        FlowPane actions = new FlowPane(8, 8, reload);
        if (saver != null) {
            Button apply = new Button("Validate and apply");
            apply.getStyleClass().add("primary-button");
            apply.setOnAction(ignored -> {
                try {
                    Json.parse(area.getText());
                    runMutation(saver.apply(area.getText()),
                            () -> loadDocument(loader.get(), area));
                } catch (RuntimeException error) {
                    showError(error);
                }
            });
            actions.getChildren().add(apply);
        } else {
            area.setEditable(false);
        }
        actions.getChildren().addAll(additionalActions);
        VBox body = new VBox(10, help, actions, area);
        body.getStyleClass().add("page-content");
        VBox.setVgrow(area, Priority.ALWAYS);
        return body;
    }

    private static Tab tab(String title, Node content) {
        Tab tab = new Tab(title, content);
        tab.setClosable(false);
        return tab;
    }

    private static TabPane tabs(Tab... tabs) {
        TabPane pane = new TabPane(tabs);
        pane.setTabClosingPolicy(TabPane.TabClosingPolicy.UNAVAILABLE);
        return pane;
    }

    private void refresh() {
        if (outlineLinkReadInFlight.compareAndSet(false, true)) {
            platformServices.takePendingOutlineAccessKey().whenComplete((value, error) -> {
                outlineLinkReadInFlight.set(false);
                if (error == null && value != null && !value.isBlank()) {
                    Platform.runLater(() -> showOutlineAccessKey(value));
                }
            });
        }
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

    private void startLiveEvents() {
        if (closed.get() || !runtime.supportsLiveEvents() ||
                liveEventSubscription.get() != null) return;
        runtime.liveEvents(RuntimeClient.EventCursor.beginning()).subscribe(
                new Flow.Subscriber<>() {
                    private Flow.Subscription subscription;

                    @Override
                    public void onSubscribe(Flow.Subscription value) {
                        subscription = value;
                        if (closed.get() || !liveEventSubscription.compareAndSet(
                                null, value)) {
                            value.cancel();
                            return;
                        }
                        value.request(Long.MAX_VALUE);
                    }

                    @Override
                    public void onNext(RuntimeClient.Event event) {
                        refresh();
                    }

                    @Override
                    public void onError(Throwable error) {
                        reconnect();
                    }

                    @Override
                    public void onComplete() {
                        reconnect();
                    }

                    private void reconnect() {
                        liveEventSubscription.compareAndSet(subscription, null);
                        if (!closed.get()) {
                            try {
                                refreshScheduler.schedule(
                                        MainView.this::startLiveEvents, 3,
                                        TimeUnit.SECONDS);
                            } catch (RejectedExecutionException ignored) {
                                // close() may win the race after the closed check.
                            }
                        }
                    }
                });
    }

    private void restartLiveEvents() {
        Flow.Subscription subscription = liveEventSubscription.getAndSet(null);
        if (subscription != null) subscription.cancel();
        if (!closed.get()) {
            try {
                refreshScheduler.execute(this::startLiveEvents);
            } catch (RejectedExecutionException ignored) {
                // close() may win the race after the closed check.
            }
        }
    }

    private void applyData(DashboardData data) {
        connectionState.setText(data.running() ? "Connected" : "Disconnected");
        setStatusClass(data.running() ? "status-connected" : "status-disconnected");
        connectButton.setText(data.running() ? "Disconnect" : "Connect");
        connectButton.setDisable(false);

        updatingProfile = true;
        profilePicker.setItems(FXCollections.observableArrayList(data.profiles()));
        profilePicker.setValue(data.activeProfile().isBlank() ? null : data.activeProfile());
        profilesList.setItems(FXCollections.observableArrayList(data.profiles()));
        profilesList.getSelectionModel().select(data.activeProfile());
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
        lastUpdated.setText("Updated just now · " + runtime.displayName());
    }

    private void showPage(Page page) {
        currentPage = page;
        pageTitle.setText(page.title);
        compactPagePicker.setValue(page);
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
        Runnable pageRefresh = pageRefreshers.get(page);
        if (pageRefresh != null) pageRefresh.run();
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
        String displayed = message == null || message.isBlank()
                ? error.toString() : message;
        errorBanner.setText(displayed);
        errorBanner.setAccessibleText("Error: " + displayed);
        errorBanner.setManaged(true);
        errorBanner.setVisible(true);
    }

    private void clearError() {
        errorBanner.setText("");
        errorBanner.setAccessibleText("");
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

    private static TextArea documentArea(String accessibleText) {
        TextArea area = new TextArea();
        area.setAccessibleText(accessibleText);
        area.setPromptText(accessibleText);
        area.setWrapText(false);
        area.setPrefRowCount(14);
        area.getStyleClass().add("document-area");
        return area;
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
        closed.set(true);
        Flow.Subscription subscription = liveEventSubscription.getAndSet(null);
        if (subscription != null) subscription.cancel();
        refreshScheduler.shutdownNow();
    }

    private enum Page {
        DASHBOARD("Dashboard", "Home"),
        PROFILES("Profiles", "Profiles"),
        ACTIVITY("Activity", "Activity"),
        ROUTING("Routing", "Routing"),
        RULES("Rules", "Rules"),
        NETWORK("Network", "Network"),
        PROMPTS("Prompts", "Prompts"),
        DEVELOPER("Developer", "Developer"),
        SETTINGS("Settings", "Settings");

        private final String title;
        private final String shortTitle;

        Page(String title, String shortTitle) {
            this.title = title;
            this.shortTitle = shortTitle;
        }

        @Override
        public String toString() {
            return title;
        }
    }
}
