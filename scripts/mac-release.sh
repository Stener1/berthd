#!/usr/bin/env bash
# Build Berth for macOS the way a release ships it, check it, and put what a
# GitHub release carries in dist/mac/:
#
#   Berth-macos-universal.dmg              the download (Apple silicon and Intel)
#   Berth-macos-universal.app.tar.gz(.sig) the in-app updater's archive and signature
#   latest.json                            the feed installed apps update from
#
#   scripts/mac-release.sh v1.2.3 [--no-notarize]
#
# The release workflow (.github/workflows/release.yml) and scripts/publish.sh
# both run this, so a release built on a Mac and one built in CI are the same.
#
# It needs, in the environment:
#   APPLE_SIGNING_IDENTITY     "Developer ID Application: …", in a keychain
#   TAURI_SIGNING_PRIVATE_KEY  the updater key (and _PASSWORD when it has one)
#   APPLE_API_ISSUER, APPLE_API_KEY, APPLE_API_KEY_PATH
#                              an App Store Connect API key, for notarization
# and the Rust targets aarch64-apple-darwin and x86_64-apple-darwin.
#
# Tauri signs the app and its berth-cli sidecar with the hardened runtime,
# notarizes the app and staples it, then makes the dmg and the updater
# archive from the stapled app. The berthd for the Mac itself
# (Contents/Resources/berthd, which Use this Mac copies out of the app and
# runs under launchd) is signed by make app-binaries with the same identity
# and the hardened runtime before Tauri seals it in, so notarization covers
# it too. This signs the dmg if Tauri did not,
# notarizes and staples it too, and fails unless every check below passes:
# codesign --verify --deep --strict, spctl (Gatekeeper's own verdict, which
# must say "Notarized Developer ID") and stapler validate, on the app, the
# dmg, the app inside the dmg and the app inside the updater archive; both
# architectures in every executable; the Mac berthd's own Developer ID
# signature, hardened runtime and timestamp, which must survive being copied
# out of the app; the version; and the updater signature against the public
# key the app carries.
#
# --no-notarize skips Apple (nothing is uploaded) and the checks that need a
# ticket, for trying a signed build locally. Its output is not releasable.
set -euo pipefail

version="${1:?usage: scripts/mac-release.sh vX.Y.Z [--no-notarize]}"
version="${version#v}"
tag="v$version"
notarize=1
[ "${2:-}" = "--no-notarize" ] && notarize=0
repo="sean-brydon/berthd"
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

die() { echo "mac-release: $*" >&2; exit 1; }
step() { printf '==> %s\n' "$*"; }
ok() { printf '  ok: %s\n' "$*"; }

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "the version must look like v1.2.3, not $1"
[ "$(uname -s)" = Darwin ] || die "this builds the macOS app, so it runs on a Mac"
: "${APPLE_SIGNING_IDENTITY:?set APPLE_SIGNING_IDENTITY to the Developer ID Application identity}"
: "${TAURI_SIGNING_PRIVATE_KEY:?set TAURI_SIGNING_PRIVATE_KEY, the updater signing key}"
export TAURI_SIGNING_PRIVATE_KEY_PASSWORD="${TAURI_SIGNING_PRIVATE_KEY_PASSWORD:-}"
if [ "$notarize" = 1 ]; then
  : "${APPLE_API_ISSUER:?set APPLE_API_ISSUER (or pass --no-notarize)}"
  : "${APPLE_API_KEY:?set APPLE_API_KEY, the API key ID (or pass --no-notarize)}"
  : "${APPLE_API_KEY_PATH:?set APPLE_API_KEY_PATH to the .p8 file (or pass --no-notarize)}"
  [ -f "$APPLE_API_KEY_PATH" ] || die "no API key at APPLE_API_KEY_PATH"
else
  # Tauri notarizes whenever these are set.
  unset APPLE_API_ISSUER APPLE_API_KEY APPLE_API_KEY_PATH APPLE_ID APPLE_PASSWORD APPLE_TEAM_ID
fi
installed="$(rustup target list --installed)"
for t in aarch64-apple-darwin x86_64-apple-darwin; do
  grep -qx "$t" <<<"$installed" || die "missing the Rust target $t: rustup target add $t"
done
# minisign checks the updater signature. CI installs it; on a Mac without it
# the check is skipped with a warning.
if ! command -v minisign >/dev/null; then
  [ -z "${CI:-}" ] || die "minisign is needed to check the updater signature"
  echo "mac-release: minisign not found (brew install minisign); not checking the updater signature" >&2
fi

target=universal-apple-darwin
bundle="app/src-tauri/target/$target/release/bundle"
out="dist/mac"
work="$(mktemp -d)"
mnt="$work/mnt"
cleanup() {
  if [ -d "$mnt" ]; then hdiutil detach -quiet "$mnt" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

step "Building Berth $version for Apple silicon and Intel"
rm -rf "$bundle"
make app-build APP_TARGET="$target" VERSION="$version"

app="$bundle/macos/Berth.app"
dmgs=("$bundle"/dmg/*.dmg)
[ -d "$app" ] || die "no $app"
if [ "${#dmgs[@]}" != 1 ] || [ ! -f "${dmgs[0]}" ]; then die "expected one dmg in $bundle/dmg"; fi
tarball="$bundle/macos/Berth.app.tar.gz"
[ -f "$tarball" ] || die "no updater archive at $tarball (is createUpdaterArtifacts on?)"
[ -f "$tarball.sig" ] || die "the updater archive was not signed"

# check_app APP: what every copy of the app must be.
check_app() {
  local app="$1" info exe
  codesign --verify --deep --strict --verbose=2 "$app" 2>&1 | sed 's/^/  /' ||
    die "$app: the signature does not verify"
  info="$(codesign -dv --verbose=2 "$app" 2>&1)"
  grep -q "^Authority=Developer ID Application:" <<<"$info" ||
    die "$app is not signed with a Developer ID (ad-hoc?); Gatekeeper would refuse it"
  if ! grep -q "^TeamIdentifier=" <<<"$info" || grep -q "^TeamIdentifier=not set" <<<"$info"; then
    die "$app has no team identifier"
  fi
  grep -Eq "^CodeDirectory .*flags=.*runtime" <<<"$info" ||
    die "$app lacks the hardened runtime, which notarization requires"
  for exe in "$app/Contents/MacOS/berth" "$app/Contents/MacOS/berth-cli" "$app/Contents/Resources/berthd"; do
    [ -x "$exe" ] || die "no $exe"
    codesign --verify --strict "$exe" || die "$exe: the signature does not verify"
    local archs
    archs="$(lipo -archs "$exe")"
    [[ " $archs " == *" arm64 "* && " $archs " == *" x86_64 "* ]] ||
      die "$exe is not universal (has: $archs)"
  done
  for d in berthd-linux-amd64 berthd-linux-arm64 tmux-linux-amd64 tmux-linux-arm64; do
    [ -f "$app/Contents/Resources/$d" ] || die "$app carries no $d"
  done
  check_berthd "$app/Contents/Resources/berthd"
  local v
  v="$(plutil -extract CFBundleShortVersionString raw "$app/Contents/Info.plist")"
  [ "$v" = "$version" ] || die "$app says it is $v, not $version"
  if [ "$notarize" = 1 ]; then
    xcrun stapler validate "$app" >/dev/null || die "$app has no stapled notarization ticket"
    gatekeeper exec "$app"
  fi
  ok "$app"
}

# check_berthd PATH: the Mac berthd runs outside the app, as its own launch
# agent, from the copy Use this Mac makes; so it needs its own Developer ID
# signature with the hardened runtime, and a copy must still verify.
check_berthd() {
  local berthd="$1" info copy
  info="$(codesign -dv --verbose=2 "$berthd" 2>&1)"
  grep -q "^Authority=Developer ID Application:" <<<"$info" ||
    die "$berthd is not signed with a Developer ID; was APPLE_SIGNING_IDENTITY set for make app-binaries?"
  if ! grep -q "^TeamIdentifier=" <<<"$info" || grep -q "^TeamIdentifier=not set" <<<"$info"; then
    die "$berthd has no team identifier"
  fi
  grep -Eq "^CodeDirectory .*flags=.*runtime" <<<"$info" ||
    die "$berthd lacks the hardened runtime, which notarization requires"
  grep -q "^Timestamp=" <<<"$info" || die "$berthd's signature has no secure timestamp"
  copy="$work/berthd-copy"
  cp "$berthd" "$copy"
  codesign --verify --strict "$copy" || die "a copy of $berthd does not verify, so Use this Mac could not run it"
  rm -f "$copy"
}

# gatekeeper TYPE PATH: spctl must accept it, as notarized.
gatekeeper() {
  local verdict
  verdict="$(spctl -a -vv -t "$1" "$2" 2>&1)" || die "Gatekeeper rejects $2: $verdict"
  if grep -q "override=security disabled" <<<"$verdict"; then
    die "Gatekeeper assessments are off on this Mac, so spctl proves nothing; turn them on with: sudo spctl --master-enable"
  fi
  grep -q "source=Notarized Developer ID" <<<"$verdict" ||
    die "Gatekeeper accepts $2 but not as notarized: $verdict"
}

step "Checking the app"
check_app "$app"

rm -rf "$out" && mkdir -p "$out"
dmg="$out/Berth-macos-universal.dmg"
cp "${dmgs[0]}" "$dmg"
cp "$tarball" "$out/Berth-macos-universal.app.tar.gz"
cp "$tarball.sig" "$out/Berth-macos-universal.app.tar.gz.sig"

step "Signing the dmg"
if codesign --verify --strict "$dmg" 2>/dev/null; then
  ok "Tauri signed it"
else
  codesign --force --timestamp --sign "$APPLE_SIGNING_IDENTITY" "$dmg"
fi
codesign --verify --strict --verbose=2 "$dmg" || die "the dmg's signature does not verify"

if [ "$notarize" = 1 ]; then
  step "Notarizing the dmg (the app inside already is)"
  result="$(xcrun notarytool submit "$dmg" \
    --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY" --issuer "$APPLE_API_ISSUER" \
    --wait --timeout 1h --output-format json)" || die "notarytool failed: $result"
  status="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))' <<<"$result")"
  if [ "$status" != Accepted ]; then
    id="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))' <<<"$result")"
    [ -z "$id" ] || xcrun notarytool log "$id" --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY" --issuer "$APPLE_API_ISSUER" >&2 || true
    die "Apple did not accept the dmg (status: ${status:-unknown})"
  fi
  ok "accepted"
  xcrun stapler staple "$dmg"
fi

step "Checking the dmg"
codesign --verify --deep --strict --verbose=2 "$dmg" || die "the dmg's signature does not verify"
if [ "$notarize" = 1 ]; then
  gatekeeper install "$dmg"
  xcrun stapler validate "$dmg" || die "the dmg has no stapled notarization ticket"
fi
mkdir -p "$mnt"
hdiutil attach -quiet -nobrowse -readonly -mountpoint "$mnt" "$dmg"
[ -L "$mnt/Applications" ] || die "the dmg has no Applications link to drag Berth to"
check_app "$mnt/Berth.app"
hdiutil detach -quiet "$mnt"
ok "$dmg"

step "Checking the updater archive"
mkdir -p "$work/tar"
tar -xzf "$out/Berth-macos-universal.app.tar.gz" -C "$work/tar"
check_app "$work/tar/Berth.app"
if command -v minisign >/dev/null; then
  pubkey="$(python3 -c 'import json; print(json.load(open("app/src-tauri/tauri.conf.json"))["plugins"]["updater"]["pubkey"])')"
  base64 -d <<<"$pubkey" >"$work/updater.pub"
  base64 -d <"$out/Berth-macos-universal.app.tar.gz.sig" >"$work/updater.minisig"
  minisign -Vq -p "$work/updater.pub" -x "$work/updater.minisig" -m "$out/Berth-macos-universal.app.tar.gz" ||
    die "the updater signature does not match the public key in tauri.conf.json, so installed apps would refuse this update"
  ok "signed with the key the app trusts"
fi

step "Writing latest.json"
python3 - "$version" "$tag" "$repo" "${NOTES:-}" "$out" <<'PY'
import datetime, json, sys
version, tag, repo, notes, out = sys.argv[1:6]
sig = open(f"{out}/Berth-macos-universal.app.tar.gz.sig").read().strip()
url = f"https://github.com/{repo}/releases/download/{tag}/Berth-macos-universal.app.tar.gz"
# One universal archive serves both kinds of Mac.
platform = {"signature": sig, "url": url}
feed = {
    "version": version,
    "notes": notes or f"Berth {version}: https://github.com/{repo}/releases/tag/{tag}",
    "pub_date": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "platforms": {"darwin-aarch64": platform, "darwin-x86_64": platform},
}
with open(f"{out}/latest.json", "w") as f:
    json.dump(feed, f, indent=2)
    f.write("\n")
PY

[ "$notarize" = 1 ] || echo "mac-release: NOT notarized (--no-notarize); for local checks only, do not publish these" >&2
(cd "$out" && shasum -a 256 ./*)
