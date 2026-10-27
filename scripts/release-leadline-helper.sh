#!/bin/sh
# Release the builds of berth-leadline, the helper the Leadline plugin runs
# on a box. It is scripts/release-helper.sh for that one helper, kept under
# the name it has always had:
#
#   scripts/release-leadline-helper.sh             build, list and upload
#   scripts/release-leadline-helper.sh --dry-run   build and list; upload nothing
#
# See scripts/release-helper.sh for what it does and what it needs.
set -eu
exec "$(dirname "$0")/release-helper.sh" berth-leadline "$@"
