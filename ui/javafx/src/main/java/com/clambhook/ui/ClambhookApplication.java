// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui;

import com.clambhook.ui.platform.PlatformServices;
import com.clambhook.ui.platform.PlatformServicesFactory;
import com.clambhook.ui.runtime.RuntimeClient;
import com.clambhook.ui.runtime.RuntimeClientFactory;
import javafx.application.Application;
import javafx.scene.Scene;
import javafx.stage.Stage;

import java.net.URL;

/** JavaFX entry point used unchanged by Gluon on Android and GNU/Linux. */
public final class ClambhookApplication extends Application {
    private RuntimeClient runtime;
    private PlatformServices platformServices;
    private MainView mainView;

    @Override
    public void start(Stage stage) {
        runtime = RuntimeClientFactory.create();
        platformServices = PlatformServicesFactory.create(runtime);
        mainView = new MainView(runtime, platformServices);
        Scene scene = new Scene(mainView.node(), 1180, 760);
        mainView.installAccelerators(scene);
        URL stylesheet = ClambhookApplication.class.getResource("clambhook.css");
        if (stylesheet != null) {
            scene.getStylesheets().add(stylesheet.toExternalForm());
        }
        stage.setTitle("ClambHook");
        stage.setMinWidth(390);
        stage.setMinHeight(620);
        stage.setScene(scene);
        stage.show();
        getParameters().getRaw().stream()
                .filter(value -> value.regionMatches(true, 0, "ss://", 0, 5) ||
                        value.regionMatches(true, 0, "ssconf://", 0, 9))
                .findFirst()
                .ifPresent(mainView::showOutlineAccessKey);
        platformServices.takePendingOutlineAccessKey().thenAccept(value -> {
            if (value != null && !value.isBlank()) {
                javafx.application.Platform.runLater(
                        () -> mainView.showOutlineAccessKey(value));
            }
        });
    }

    @Override
    public void stop() {
        if (mainView != null) {
            mainView.close();
        }
        if (platformServices != null) {
            platformServices.close();
        }
        if (runtime != null) {
            runtime.close();
        }
    }

    public static void main(String[] args) {
        launch(args);
    }
}
