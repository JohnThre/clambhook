// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime;

import com.clambhook.ui.backend.BackendFactory;

/** Creates the shared client over the appropriate platform transport. */
public final class RuntimeClientFactory {
    private RuntimeClientFactory() {
    }

    public static RuntimeClient create() {
        return new DefaultRuntimeClient(BackendFactory.create());
    }
}
