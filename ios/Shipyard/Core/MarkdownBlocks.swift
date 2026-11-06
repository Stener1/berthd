import Foundation

/// A reply's Markdown cut into the blocks the chat draws: fenced code as it
/// is, everything else as inline Markdown with its line breaks kept.
enum MarkdownBlock: Equatable {
    case code(String)
    case text(String)

    static func parse(_ source: String) -> [MarkdownBlock] {
        var blocks: [MarkdownBlock] = []
        var prose: [String] = []
        var code: [String] = []
        var fence: String?
        func flush() {
            let t = prose.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
            if !t.isEmpty { blocks.append(.text(t)) }
            prose = []
        }
        for line in source.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if let open = fence {
                if trimmed.hasPrefix(open) {
                    blocks.append(.code(code.joined(separator: "\n")))
                    code = []
                    fence = nil
                } else {
                    code.append(line)
                }
            } else if trimmed.hasPrefix("```") || trimmed.hasPrefix("~~~") {
                flush()
                fence = String(trimmed.prefix(3))
            } else {
                prose.append(line)
            }
        }
        if fence != nil { blocks.append(.code(code.joined(separator: "\n"))) }
        flush()
        return blocks
    }

    /// Inline Markdown for a text block; a heading line becomes bold.
    static func attributed(_ text: String) -> AttributedString {
        let lines = text.components(separatedBy: "\n").map { line -> String in
            let hashes = line.prefix { $0 == "#" }
            guard (1...6).contains(hashes.count), line.dropFirst(hashes.count).first == " " else { return line }
            return "**\(line.dropFirst(hashes.count + 1))**"
        }
        let options = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: lines.joined(separator: "\n"), options: options)) ?? AttributedString(text)
    }
}
