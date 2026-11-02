import Foundation

/// A box as the desktop app hands it over in a pairing link.
struct PairedBox: Codable, Equatable {
    var name: String
    var url: URL
    var token: String
}

enum PairingError: Error, Equatable {
    case noPairing
    case damaged
}

/// A pairing link is `http://<box>:1379/#pair=<base64url of [{name,url,token}]>`
/// (app/src/lib/phone.ts). The data lives in the fragment, so it never reaches
/// a server.
enum PairingLink {
    static func parse(_ text: String) throws -> [PairedBox] {
        guard let range = text.range(of: "#pair=") else { throw PairingError.noPairing }
        let payload = text[range.upperBound...].prefix { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" }
        guard !payload.isEmpty, let data = base64URLDecode(String(payload)) else { throw PairingError.damaged }
        struct Raw: Decodable { var name: String?; var url: String; var token: String }
        guard let raw = try? JSONDecoder().decode([Raw].self, from: data) else { throw PairingError.damaged }
        let boxes = raw.compactMap { r -> PairedBox? in
            guard !r.token.isEmpty, let url = normalized(r.url) else { return nil }
            let name = (r.name?.isEmpty == false ? r.name : nil) ?? url.host() ?? "box"
            return PairedBox(name: name, url: url, token: r.token)
        }
        guard !boxes.isEmpty else { throw PairingError.damaged }
        return boxes
    }

    /// Scheme, host and port only: the box answers at its origin.
    static func normalized(_ s: String) -> URL? {
        guard var c = URLComponents(string: s.trimmingCharacters(in: .whitespaces)),
              let scheme = c.scheme?.lowercased(), scheme == "http" || scheme == "https",
              c.host?.isEmpty == false else { return nil }
        c.path = ""
        c.query = nil
        c.fragment = nil
        c.user = nil
        c.password = nil
        return c.url
    }

    static func base64URLDecode(_ s: String) -> Data? {
        var b = s.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while b.count % 4 != 0 { b += "=" }
        return Data(base64Encoded: b)
    }

    static func base64URLEncode(_ data: Data) -> String {
        data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
