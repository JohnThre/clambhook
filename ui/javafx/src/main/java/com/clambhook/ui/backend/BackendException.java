// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

/** A stable error carrying the platform response code when one exists. */
public final class BackendException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    private final int statusCode;

    public BackendException(String message) {
        this(0, message, null);
    }

    public BackendException(int statusCode, String message) {
        this(statusCode, message, null);
    }

    public BackendException(int statusCode, String message, Throwable cause) {
        super(message == null || message.isBlank() ? "ClambHook request failed" : message, cause);
        this.statusCode = statusCode;
    }

    public int statusCode() {
        return statusCode;
    }
}
