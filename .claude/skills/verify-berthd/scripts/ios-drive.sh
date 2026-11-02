#!/usr/bin/env bash
# Drive the iPhone app in a simulator against live boxes. See features/ios-app.md.
#   ios-drive.sh pair                      pair from $PAIR_FILE (default /tmp/shipyard-pair.txt)
#   ios-drive.sh session BOX NAME [TEXT]   open a session, send TEXT, press Esc
#   ios-drive.sh chat BOX NAME OPTION      open a session's chat, tap the ask card's OPTION, send a prompt, show the screen
#   ios-drive.sh gate                      open Runs, then the first run
# SIM is the simulator's UDID (xcrun simctl list devices); it is required.
set -euo pipefail
: "${SIM:?set SIM to the simulator udid}"
PAIR_FILE=${PAIR_FILE:-/tmp/shipyard-pair.txt}
ROOT=$(cd "$(dirname "$0")/../../../.." && pwd)
APP=$ROOT/ios/build/DerivedData/Build/Products/Debug-iphonesimulator/Shipyard.app
ID=dev.shipyard.phone
FLOW=$(mktemp -d)
trap 'rm -rf "$FLOW"' EXIT

launch() { xcrun simctl terminate "$SIM" $ID 2>/dev/null || true; xcrun simctl launch "$SIM" $ID "$@" >/dev/null; sleep 6; }
shot() { xcrun simctl io "$SIM" screenshot "${OUT:-/tmp/ios-shots}/$1.png" 2>/dev/null; }
flow() { printf 'appId: %s\n---\n%s\n' $ID "$1" > "$FLOW/f.yaml"; maestro --device "$SIM" test "$FLOW/f.yaml" | grep -E "COMPLETED|FAILED"; }
mkdir -p "${OUT:-/tmp/ios-shots}"

case "${1:-}" in
pair)
  xcrun simctl install "$SIM" "$APP"
  launch -ShipyardOpenURL "shipyard://pair$(sed 's/.*#/#/' "$PAIR_FILE")"
  shot board ;;
session)
  launch -ShipyardOpenURL "shipyard://box/$2/session/$3"
  shot session
  if [ -n "${4:-}" ]; then
    flow "- tapOn: \"Send a prompt\"
- inputText: \"$4\"
- tapOn: \"Send\"
- waitForAnimationToEnd"
    sleep 12
  fi
  flow '- tapOn: "Esc"'
  shot session-after ;;
gate)
  launch
  flow '- tapOn: "Runs"
- waitForAnimationToEnd
- tapOn:
    point: "50%,20%"
- waitForAnimationToEnd'
  shot run ;;
*) sed -n 2,6p "$0"; exit 2 ;;
esac
