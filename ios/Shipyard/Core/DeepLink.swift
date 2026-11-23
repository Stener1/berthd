import Foundation

struct SessionTarget: Equatable, Hashable {
    var box: String?
    var session: String
}

/// `shipyard://box/<box>/session/<name>`, or the link ntfy opens on a tap:
/// the box's own address with `#s=<session>` (internal/box/phone.go notify).
enum DeepLink {
    static func parse(_ url: URL) -> SessionTarget? {
        if url.scheme?.lowercased() == "shipyard" {
            guard url.host() == "box" else { return nil }
            let parts = url.pathComponents.filter { $0 != "/" }
            guard parts.count == 3, parts[1] == "session" else { return nil }
            return SessionTarget(box: parts[0], session: parts[2])
        }
        guard let fragment = url.fragment, fragment.hasPrefix("s="), fragment.count > 2 else { return nil }
        let raw = String(fragment.dropFirst(2))
        let session = raw.removingPercentEncoding ?? raw
        if let slash = session.firstIndex(of: "/") {
            return SessionTarget(box: String(session[..<slash]), session: String(session[session.index(after: slash)...]))
        }
        return SessionTarget(box: nil, session: session)
    }
}
