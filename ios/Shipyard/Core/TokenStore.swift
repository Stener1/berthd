import Foundation
import Security

/// Where a box's token lives. The Keychain in the app, memory in tests.
protocol TokenStore: AnyObject {
    func token(for id: UUID) -> String?
    func setToken(_ token: String, for id: UUID)
    func removeToken(for id: UUID)
}

final class KeychainTokenStore: TokenStore {
    private let service: String

    init(service: String = "phone-box-token") { self.service = service }

    private func query(_ id: UUID) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: id.uuidString]
    }

    func token(for id: UUID) -> String? {
        var q = query(id)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else { return nil }
        return String(data: d, encoding: .utf8)
    }

    func setToken(_ token: String, for id: UUID) {
        SecItemDelete(query(id) as CFDictionary)
        var q = query(id)
        q[kSecValueData as String] = Data(token.utf8)
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }

    func removeToken(for id: UUID) { SecItemDelete(query(id) as CFDictionary) }
}

final class MemoryTokenStore: TokenStore {
    private var tokens: [UUID: String] = [:]
    func token(for id: UUID) -> String? { tokens[id] }
    func setToken(_ token: String, for id: UUID) { tokens[id] = token }
    func removeToken(for id: UUID) { tokens[id] = nil }
}
