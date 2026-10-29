import Foundation
import Observation

/// A paired box. Its token is not here: it is in the Keychain under `id`.
struct Box: Codable, Equatable, Identifiable, Hashable {
    var id: UUID
    var name: String
    var url: URL
}

/// The paired boxes: names and addresses in UserDefaults, tokens in the Keychain.
@Observable
final class BoxStore {
    private(set) var boxes: [Box] = []
    private let defaults: UserDefaults
    private let tokens: TokenStore
    private let key = "boxes.v1"

    init(defaults: UserDefaults = .standard, tokens: TokenStore = KeychainTokenStore()) {
        self.defaults = defaults
        self.tokens = tokens
        if let d = defaults.data(forKey: key), let b = try? JSONDecoder().decode([Box].self, from: d) {
            boxes = b.filter { tokens.token(for: $0.id) != nil }
        }
    }

    private func save() {
        defaults.set(try? JSONEncoder().encode(boxes), forKey: key)
    }

    func token(for box: Box) -> String? { tokens.token(for: box.id) }

    /// Adds boxes from a pairing link. A box at an address already paired keeps
    /// its place and name and gets the new token, which is how re-pairing works.
    @discardableResult
    func pair(_ paired: [PairedBox]) -> [Box] {
        var out: [Box] = []
        for p in paired {
            if let i = boxes.firstIndex(where: { $0.url == p.url }) {
                tokens.setToken(p.token, for: boxes[i].id)
                out.append(boxes[i])
            } else {
                let b = Box(id: UUID(), name: p.name, url: p.url)
                tokens.setToken(p.token, for: b.id)
                boxes.append(b)
                out.append(b)
            }
        }
        save()
        return out
    }

    func rename(_ box: Box, to name: String) {
        let n = name.trimmingCharacters(in: .whitespaces)
        guard !n.isEmpty, let i = boxes.firstIndex(of: box) else { return }
        boxes[i].name = n
        save()
    }

    /// Forgets the box and its token.
    func remove(_ box: Box) {
        tokens.removeToken(for: box.id)
        boxes.removeAll { $0.id == box.id }
        save()
    }

    func client(for box: Box) -> (any PhoneAPIClient)? {
        guard let t = tokens.token(for: box.id) else { return nil }
        return URLSessionPhoneClient(base: box.url, token: t)
    }
}
