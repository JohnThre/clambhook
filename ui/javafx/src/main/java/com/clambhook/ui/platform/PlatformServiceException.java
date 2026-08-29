// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

/** Stable failure type for platform-owned operations. */
public final class PlatformServiceException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public PlatformServiceException(String message) {
        super(message == null || message.isBlank() ? "platform operation failed" : message);
    }

    public PlatformServiceException(String message, Throwable cause) {
        super(message == null || message.isBlank() ? "platform operation failed" : message, cause);
    }
}
