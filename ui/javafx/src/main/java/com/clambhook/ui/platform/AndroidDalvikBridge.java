// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import java.util.Objects;

/** JNI boundary from the Gluon native image into the Android platform AAR. */
public final class AndroidDalvikBridge {
    private AndroidDalvikBridge() {
    }

    public static native String request(String method, String path, String body);

    public static native String dispatch(String operation, String requestJson);

    public static String safeRequest(String method, String path, String body) {
        return Objects.requireNonNullElse(request(
                Objects.requireNonNullElse(method, "GET"),
                Objects.requireNonNullElse(path, "/"),
                Objects.requireNonNullElse(body, "")), "");
    }

    public static String safeDispatch(String operation, String requestJson) {
        return Objects.requireNonNullElse(dispatch(
                Objects.requireNonNullElse(operation, ""),
                Objects.requireNonNullElse(requestJson, "{}")), "");
    }
}
