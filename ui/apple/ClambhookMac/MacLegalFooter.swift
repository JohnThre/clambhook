import SwiftUI

// MARK: - Legal footer

/// Non-intrusive legal footer shown at the bottom of license/about surfaces.
struct LegalFooter: View {
    var body: some View {
        Text("© 2025 Pengfan Chang. All rights reserved. Confidential and proprietary. support@swiphtgroup.com")
            .font(.caption)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}
