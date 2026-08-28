// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import SwiftUI

// MARK: - Legal footer

/// Non-intrusive legal footer shown at the bottom of license/about surfaces.
struct LegalFooter: View {
    var body: some View {
        Text("© 2026 Pengfan Chang. GPL-3.0-only source; commercial licensing: support@swiphtgroup.com")
            .font(.caption)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}
