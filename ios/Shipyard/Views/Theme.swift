import SwiftUI

/// The desktop app's neutral palette (app/src/index.css) and the phone page's
/// state colours, light and dark.
enum Theme {
    static func dynamic(light: UInt32, dark: UInt32) -> Color {
        Color(UIColor { $0.userInterfaceStyle == .dark ? UIColor(hex: dark) : UIColor(hex: light) })
    }

    static let background = dynamic(light: 0xF6F6F4, dark: 0x16181B)
    static let card = dynamic(light: 0xFFFFFF, dark: 0x1D2023)
    static let sunk = dynamic(light: 0xEFEEEB, dark: 0x23262A)
    static let line = dynamic(light: 0xE2E1DD, dark: 0x2C2F34)
    static let muted = dynamic(light: 0x686B70, dark: 0x9A9CA1)
    static let terminal = dynamic(light: 0x1B1D20, dark: 0x111315)
    static let terminalText = Color(UIColor(hex: 0xD9D7D2))

    static let waiting = dynamic(light: 0xB7791F, dark: 0xE8A94B)
    static let working = dynamic(light: 0x2C7A73, dark: 0x5FC4B8)
    static let done = dynamic(light: 0x2F7D4F, dark: 0x6FCF97)
    static let idle = Color(UIColor(hex: 0x8A8D92))
    static let bad = dynamic(light: 0xC2412D, dark: 0xF07A66)

    static func color(_ s: BoardState) -> Color {
        switch s {
        case .waiting: waiting
        case .working: working
        case .done: done
        case .ready, .exited: idle
        }
    }
}

extension UIColor {
    convenience init(hex: UInt32) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

struct StateBadge: View {
    var text: String
    var color: Color

    var body: some View {
        HStack(spacing: 5) {
            Circle().fill(color).frame(width: 7, height: 7)
            Text(text).font(.caption.weight(.medium)).foregroundStyle(color)
        }
        .padding(.horizontal, 8).padding(.vertical, 3)
        .background(color.opacity(0.12), in: Capsule())
        .accessibilityElement(children: .combine)
    }
}

func relative(_ date: Date?) -> String {
    guard let date else { return "" }
    let s = max(0, Int(Date().timeIntervalSince(date)))
    if s < 60 { return "now" }
    if s < 3600 { return "\(s / 60)m" }
    if s < 86400 { return "\(s / 3600)h" }
    return "\(s / 86400)d"
}

let agentNames = ["claude": "Claude Code", "codex": "Codex", "gemini": "Gemini", "cursor": "Cursor", "opencode": "OpenCode"]
