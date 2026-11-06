import Testing
import Foundation
@testable import Shipyard

struct PairingLinkTests {
    private func link(_ json: String, origin: String = "http://100.64.1.2:1379") -> String {
        origin + "/#pair=" + PairingLink.base64URLEncode(Data(json.utf8))
    }

    @Test func parsesEveryBoxInTheLink() throws {
        let json = #"[{"name":"devbox","url":"http://100.64.1.2:1379/","token":"aa"},{"name":"mini","url":"http://acme-mini.tail1234.ts.net:1379","token":"bb"}]"#
        let boxes = try PairingLink.parse(link(json))
        #expect(boxes.map(\.name) == ["devbox", "mini"])
        #expect(boxes[0].url.absoluteString == "http://100.64.1.2:1379")
        #expect(boxes[1].token == "bb")
    }

    @Test func parsesUnicodeNames() throws {
        let boxes = try PairingLink.parse(link(#"[{"name":"høyloft ✓","url":"http://100.64.1.2:1379","token":"aa"}]"#))
        #expect(boxes[0].name == "høyloft ✓")
    }

    @Test func nameDefaultsToTheHost() throws {
        let boxes = try PairingLink.parse(link(#"[{"url":"http://100.64.1.2:1379","token":"aa"}]"#))
        #expect(boxes[0].name == "100.64.1.2")
    }

    @Test func rejectsTextWithoutAPairing() {
        #expect(throws: PairingError.noPairing) { try PairingLink.parse("http://100.64.1.2:1379/") }
    }

    @Test func rejectsDamagedLinks() {
        #expect(throws: PairingError.damaged) { try PairingLink.parse("http://x/#pair=!!!") }
        #expect(throws: PairingError.damaged) { try PairingLink.parse("http://x/#pair=bm90IGpzb24") }
        #expect(throws: PairingError.damaged) { try PairingLink.parse(link(#"[{"url":"ftp://x","token":"aa"}]"#)) }
        #expect(throws: PairingError.damaged) { try PairingLink.parse(link(#"[{"url":"http://x:1379","token":""}]"#)) }
    }

    @Test func toleratesSurroundingText() throws {
        let l = link(#"[{"name":"a","url":"http://100.64.1.2:1379","token":"aa"}]"#)
        #expect(try PairingLink.parse("  here: \(l)\n").count == 1)
    }
}

struct BoxStoreTests {
    private func store() -> (BoxStore, MemoryTokenStore) {
        let tokens = MemoryTokenStore()
        let defaults = UserDefaults(suiteName: UUID().uuidString)!
        return (BoxStore(defaults: defaults, tokens: tokens), tokens)
    }

    private let devbox = PairedBox(name: "devbox", url: URL(string: "http://100.64.1.2:1379")!, token: "aa")

    @Test func pairKeepsTokensOutOfThePersistedList() {
        let (s, tokens) = store()
        let b = s.pair([devbox])[0]
        #expect(tokens.token(for: b.id) == "aa")
        #expect(s.client(for: b) != nil)
    }

    @Test func pairingTheSameAddressAgainReplacesTheTokenOnly() {
        let (s, _) = store()
        let first = s.pair([devbox])[0]
        s.rename(first, to: "my devbox")
        let again = s.pair([PairedBox(name: "devbox", url: devbox.url, token: "new")])[0]
        #expect(s.boxes.count == 1)
        #expect(again.id == first.id)
        #expect(s.boxes[0].name == "my devbox")
        #expect(s.token(for: again) == "new")
    }

    @Test func removeForgetsTheToken() {
        let (s, tokens) = store()
        let b = s.pair([devbox])[0]
        s.remove(b)
        #expect(s.boxes.isEmpty)
        #expect(tokens.token(for: b.id) == nil)
    }

    @Test func emptyRenameIsIgnored() {
        let (s, _) = store()
        let b = s.pair([devbox])[0]
        s.rename(b, to: "   ")
        #expect(s.boxes[0].name == "devbox")
    }

    @Test func boxesPersistAcrossLaunches() {
        let tokens = MemoryTokenStore()
        let defaults = UserDefaults(suiteName: UUID().uuidString)!
        let first = BoxStore(defaults: defaults, tokens: tokens)
        first.pair([devbox])
        let second = BoxStore(defaults: defaults, tokens: tokens)
        #expect(second.boxes.map(\.name) == ["devbox"])
    }
}
