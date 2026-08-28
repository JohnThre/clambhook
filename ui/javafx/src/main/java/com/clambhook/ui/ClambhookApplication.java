// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui;

import com.clambhook.ui.backend.Backend;
import com.clambhook.ui.backend.BackendFactory;
import javafx.application.Application;
import javafx.scene.Scene;
import javafx.stage.Stage;

import java.net.URL;

/** JavaFX entry point used unchanged by Gluon on Android and GNU/Linux. */
public final class ClambhookApplication extends Application {
    private Backend backend;
    private MainView mainView;

    @Override
    public void start(Stage stage) {
        backend = BackendFactory.create();
        mainView = new MainView(backend);
        Scene scene = new Scene(mainView.node(), 1180, 760);
        URL stylesheet = ClambhookApplication.class.getResource("clambhook.css");
        if (stylesheet != null) {
            scene.getStylesheets().add(stylesheet.toExternalForm());
        }
        stage.setTitle("ClambHook");
        stage.setMinWidth(390);
        stage.setMinHeight(620);
        stage.setScene(scene);
        stage.show();
    }

    @Override
    public void stop() {
        if (mainView != null) {
            mainView.close();
        }
        if (backend != null) {
            backend.close();
        }
    }

    public static void main(String[] args) {
        launch(args);
    }
}
