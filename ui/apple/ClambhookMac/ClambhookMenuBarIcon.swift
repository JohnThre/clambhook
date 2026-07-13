import SwiftUI

/// Custom monochrome menu bar glyph for clambhook, styled after the app's
/// hook/clam brand mark (Surge/Little Snitch-style status item icon) instead
/// of a generic SF Symbol. Renders filled when the daemon is running and as
/// an outline with a diagonal slash when it is stopped, mirroring the
/// semantics of the previous "network" / "network.slash" pairing.
struct ClambhookMenuBarIcon: View {
    let isActive: Bool

    var body: some View {
        ZStack {
            if isActive {
                ClambhookBodyShape().fill(Color.primary)
                ClambhookFlagShape().fill(Color.primary)
            } else {
                ClambhookBodyShape().stroke(Color.primary, lineWidth: 1.3)
                ClambhookFlagShape().stroke(Color.primary, lineWidth: 1.1)
            }
            ClambhookHookShape()
                .stroke(Color.primary, style: StrokeStyle(lineWidth: 1.4, lineCap: .round, lineJoin: .round))
            if !isActive {
                ClambhookSlashShape()
                    .stroke(Color.primary, style: StrokeStyle(lineWidth: 1.3, lineCap: .round))
            }
        }
        .frame(width: 16, height: 16)
        .accessibilityLabel(Text("clambhook"))
    }
}

private extension CGRect {
    func fractionalPoint(_ fx: CGFloat, _ fy: CGFloat) -> CGPoint {
        CGPoint(x: minX + fx * width, y: minY + fy * height)
    }
}

/// The rounded "clam" body, bulging on the right with a tapered top/bottom.
private struct ClambhookBodyShape: Shape {
    func path(in rect: CGRect) -> Path {
        let r = rect.insetBy(dx: rect.width * 0.10, dy: rect.height * 0.04)
        var path = Path()
        path.move(to: r.fractionalPoint(0.46, 0.32))
        path.addCurve(
            to: r.fractionalPoint(0.90, 0.62),
            control1: r.fractionalPoint(0.80, 0.30),
            control2: r.fractionalPoint(0.94, 0.44)
        )
        path.addCurve(
            to: r.fractionalPoint(0.50, 0.94),
            control1: r.fractionalPoint(0.86, 0.88),
            control2: r.fractionalPoint(0.66, 0.96)
        )
        path.addCurve(
            to: r.fractionalPoint(0.46, 0.32),
            control1: r.fractionalPoint(0.32, 0.80),
            control2: r.fractionalPoint(0.32, 0.46)
        )
        path.closeSubpath()
        return path
    }
}

/// The thin curved hook rising from the top of the body, echoing the
/// hook/clasp motif of the app icon.
private struct ClambhookHookShape: Shape {
    func path(in rect: CGRect) -> Path {
        let r = rect.insetBy(dx: rect.width * 0.10, dy: rect.height * 0.04)
        var path = Path()
        path.move(to: r.fractionalPoint(0.42, 0.32))
        path.addCurve(
            to: r.fractionalPoint(0.32, 0.08),
            control1: r.fractionalPoint(0.20, 0.28),
            control2: r.fractionalPoint(0.18, 0.16)
        )
        return path
    }
}

/// The small diamond "flag" capping the hook.
private struct ClambhookFlagShape: Shape {
    func path(in rect: CGRect) -> Path {
        let r = rect.insetBy(dx: rect.width * 0.10, dy: rect.height * 0.04)
        var path = Path()
        path.move(to: r.fractionalPoint(0.32, 0.00))
        path.addLine(to: r.fractionalPoint(0.42, 0.07))
        path.addLine(to: r.fractionalPoint(0.32, 0.14))
        path.addLine(to: r.fractionalPoint(0.22, 0.07))
        path.closeSubpath()
        return path
    }
}

/// Diagonal slash overlaid when the daemon is stopped, matching the
/// "network.slash" convention it replaces.
private struct ClambhookSlashShape: Shape {
    func path(in rect: CGRect) -> Path {
        var path = Path()
        path.move(to: CGPoint(x: rect.minX + rect.width * 0.12, y: rect.maxY - rect.height * 0.12))
        path.addLine(to: CGPoint(x: rect.maxX - rect.width * 0.12, y: rect.minY + rect.height * 0.12))
        return path
    }
}
