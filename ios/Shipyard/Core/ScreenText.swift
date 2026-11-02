import Foundation

enum ScreenText {
    /// The screen text with every URL made a link, which Safari opens.
    static func linked(_ text: String) -> AttributedString {
        var out = AttributedString(text)
        guard let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else { return out }
        let ns = text as NSString
        for m in detector.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            guard let url = m.url, let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
                  let r = Range(m.range, in: text), let lo = AttributedString.Index(r.lowerBound, within: out),
                  let hi = AttributedString.Index(r.upperBound, within: out) else { continue }
            out[lo..<hi].link = url
        }
        return out
    }

    static func urls(in text: String) -> [URL] {
        guard let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else { return [] }
        return detector.matches(in: text, range: NSRange(location: 0, length: (text as NSString).length))
            .compactMap(\.url).filter { ["http", "https"].contains($0.scheme?.lowercased() ?? "") }
    }

    /// The last non-empty lines, for a card's preview.
    static func tail(_ screen: String, lines n: Int = 3) -> String {
        let lines = screen.split(separator: "\n", omittingEmptySubsequences: true)
            .map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        return lines.suffix(n).joined(separator: "\n")
    }
}
