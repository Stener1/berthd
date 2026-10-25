# Shipyard for iPhone

A native SwiftUI client of the box's [phone API](../docs/guides/phone.mdx)
(`/phone/v1/*`, port 1379, on the tailnet). It does what the phone web app does,
as a real app: every agent from every box on one board, a session's screen with a
message box and quick keys, and runs with their gates.

It talks only to the existing phone API. Nothing on a box changes.

## Build

You need Xcode and [xcodegen](https://github.com/yonaskolb/XcodeGen)
(`brew install xcodegen`). The Xcode project is generated from `project.yml`
and not kept in git.

```sh
make ios-test     # unit tests, on an iPhone simulator
make ios-build    # build for the simulator
open ios/Shipyard.xcodeproj   # after `make ios-project`
```

`IOS_SIM="iPhone 17 Pro"` picks the simulator. Derived data goes to
`ios/build/`; `make ios-clean` deletes it.

## Pairing

In Shipyard on your computer: **Settings → Phone**, turn on phone access for each
box. Then either scan the code with the app, or **Copy link** and paste it under
*Or paste a pairing link*. One link pairs every box that has phone access on.
Tokens are kept in the iOS Keychain; removing a box forgets its token. If a
token is replaced (**New code** in Settings), the box shows *Re-pair this box*.

Your phone needs Tailscale, signed in to the same tailnet.

## Install on your own iPhone (cable)

```sh
cp ios/Config/Local.xcconfig.example ios/Config/Local.xcconfig
# edit it: your Apple development team, and a bundle id of your own
xcrun devicectl list devices
make ios-install DEVICE="<name or udid>"
```

`Local.xcconfig` is gitignored, so no team or bundle id is committed. Signing is
automatic.

## Network access (App Transport Security)

Boxes answer plain HTTP inside the tailnet (WireGuard encrypts it). iOS offers no
ATS exception for an address range, and the narrower ones do not reach a tailnet
address: `NSAllowsLocalNetworking` covers private and `.local` hosts only, and a
tailnet `100.x` address is neither (the simulator fails with error -1022). An
exception domain needs the exact host, which differs for every user and cannot be
committed. So `project.yml` sets `NSAllowsArbitraryLoads`.

What bounds it: the app builds requests only from the box addresses you paired
(`BoxStore`), and opens links in screen text in Safari, not in the app. If you
want it tighter for your own phone, replace it with an `NSExceptionDomains`
entry per box address in your copy of `project.yml`.

## Deep links

`shipyard://box/<box>/session/<name>` opens a session. ntfy's tap link (the box's
address with `#s=<session>`) opens it too when it is opened in the app.

## Layout

- `Shipyard/Core`: models, `PhoneAPIClient` (protocol + URLSession implementation),
  pairing-link parsing, Keychain token store, board sorting and polling.
- `Shipyard/Views`: SwiftUI screens.
- `ShipyardTests`: Swift Testing. `Fixtures/` are response shapes with synthetic names.
