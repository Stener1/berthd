#!/bin/sh
# Release the builds of a helper this repository makes: a program a plugin
# runs on a box. Build it for every box platform, list those very files in
# the plugin listing by their checksums, and attach them to a GitHub release
# of their own.
#
#   scripts/release-helper.sh NAME             build, list and upload
#   scripts/release-helper.sh NAME --dry-run   build and list; upload nothing
#
# NAME is a helper of scripts/helpers.json, by its program's name
# (berth-leadline, berth-database) or its plugin's (leadline, database).
# That file is the table of them: for each, the plugin whose manifest names
# it, where its command is, the make target that builds it for every
# platform, and what its releases are called. A new helper is a row there
# and a target in the Makefile; app/scripts/build-marketplace.mjs reads the
# same table.
#
# The version is box.helper.version in plugins/<plugin>/berth-plugin.json.
# Bump it there first: a version is released once. People allow the plugin's
# manifest, which names each build by its SHA-256, so the files of a release
# never change; new builds are a new version, and the listing build refuses
# other checksums under one that is listed.
#
# What it does, in order:
#
#   1. make <target> VERSION=<version>: bin/<name>-<os>-<arch> for linux and
#      darwin, amd64 and arm64, static, stamped v<version>.
#   2. app/scripts/build-marketplace.mjs --helper-bin bin --helper <name>:
#      site/plugins/ with each build's size and SHA-256, at
#      https://github.com/<repo>/releases/download/<release>-v<version>/<name>-<os>-<arch>
#      The other helpers' builds are left as the listing has them.
#   3. gh release create <release>-v<version> with the four files and their
#      checksums.txt. It is not marked as the latest release: the latest is
#      Shipyard's own, which site/install.sh and the app's updater download
#      from.
#
# Then commit site/plugins/ and merge it: the listing on the public site is
# what makes the new builds installable. Until then nothing uses them.
#
# It needs go, node (with app/node_modules installed), make and gh (signed
# in, with write access to the repository). The commit it builds has to be
# on GitHub already, because the release's tag points at it.
#
#   REPO=owner/name   another repository than cosscom/shipyard
#   VERSION=1.2.3     build this version instead of the plugin manifest's (with
#                     --dry-run only: a release is what the manifest says)
set -eu

repo=${REPO:-cosscom/shipyard}
platforms="linux-amd64 linux-arm64 darwin-arm64 darwin-amd64"
usage="usage: scripts/release-helper.sh NAME [--dry-run]"
asked=${1:-}
dry=
case "${2:-}" in
"") ;;
--dry-run) dry=1 ;;
*)
	echo "$usage" >&2
	exit 2
	;;
esac
case "$asked" in
"" | -*)
	echo "$usage" >&2
	exit 2
	;;
esac

die() {
	echo "release-helper: $*" >&2
	exit 1
}

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
table=scripts/helpers.json

for tool in go node make; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
done
[ -f "$table" ] || die "$table is not there: it lists the helpers this repository builds"

# The helper's row, one field to a line: its program's name, its plugin, its
# make target, what its releases are called and its title.
row=$(node -e '
	const table = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
	const asked = process.argv[2];
	const name = table[asked] ? asked : Object.keys(table).find((n) => table[n].plugin === asked);
	if (!name) {
		console.error("release-helper: " + asked + " is not a helper of " + process.argv[1] + " (" + Object.keys(table).join(", ") + ")");
		process.exit(1);
	}
	const h = table[name];
	for (const field of [name, h.plugin, h.make, h.release, h.title]) {
		if (typeof field !== "string" || !/^[A-Za-z0-9][A-Za-z0-9 ._-]*$/.test(field)) {
			console.error("release-helper: the row of " + name + " in " + process.argv[1] + " is incomplete");
			process.exit(1);
		}
		console.log(field);
	}
' "$table" "$asked") || exit 1
name=$(echo "$row" | sed -n 1p)
plugin=$(echo "$row" | sed -n 2p)
target=$(echo "$row" | sed -n 3p)
release=$(echo "$row" | sed -n 4p)
title=$(echo "$row" | sed -n 5p)

manifest=plugins/$plugin/berth-plugin.json
[ -f "$manifest" ] || die "$manifest is not there: the $plugin plugin names the helper's version"
[ -d app/node_modules ] || die "app/node_modules is missing: run (cd app && pnpm install --frozen-lockfile)"

listed=$(node -e '
	const m = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
	const h = (m.box || {}).helper || {};
	if (h.name !== process.argv[2] || !h.version) process.exit(1);
	console.log(h.version);
' "$manifest" "$name") || die "$manifest has no box.helper named $name with a version"
version=${VERSION:-$listed}
case "$version" in
"" | v* | *[!0-9A-Za-z.+-]*) die "\"$version\" is not a version like 0.1.0 (no leading v)" ;;
esac
if [ "$version" != "$listed" ] && [ -z "$dry" ]; then
	die "VERSION=$version is not the $listed that $manifest names; a release is what the manifest says"
fi
tag="$release-v$version"

if [ -z "$dry" ]; then
	command -v gh >/dev/null 2>&1 || die "gh is not installed"
	gh auth status >/dev/null 2>&1 || die "gh is not signed in: run gh auth login"
	# What is built is what is committed: the tag points at this commit.
	[ -z "$(git status --porcelain -- . ':!site/plugins')" ] || die "there are uncommitted changes; commit them first"
	[ -n "$(git branch -r --contains HEAD 2>/dev/null)" ] || die "this commit is not on GitHub yet; push it first"
	if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
		die "$tag is already released. Its files never change: bump box.helper.version in $manifest"
	fi
fi

echo "Building $name $version for: $platforms"
make "$target" VERSION="$version"
files=
names=
for p in $platforms; do
	[ -s "bin/$name-$p" ] || die "bin/$name-$p was not built"
	files="$files bin/$name-$p"
	names="$names $name-$p"
done

echo "Listing them in site/plugins/"
node app/scripts/build-marketplace.mjs --helper-bin bin --helper "$name"

# As sha256sum prints it, by the names the release's files have.
sums="bin/$name-checksums.txt"
# shellcheck disable=SC2086 # names is a list of file names with no spaces in them
if command -v sha256sum >/dev/null 2>&1; then
	(cd bin && sha256sum -- $names) >"$sums"
else
	(cd bin && shasum -a 256 -- $names) >"$sums"
fi
cat "$sums"

if [ -n "$dry" ]; then
	echo
	echo "Dry run: nothing was uploaded. site/plugins/ now lists these builds; do not commit it"
	echo "(git checkout -- site/plugins), since a release builds them again."
	exit 0
fi

commit=$(git rev-parse HEAD)
notes="The builds of $name $version, the helper the $plugin plugin runs on a box.

Shipyard installs these itself: turn the plugin on for a box and the app
downloads the build for that box, checks it against the SHA-256 in the
plugin's manifest, and uploads it. There is nothing here to install by hand.

Built from $commit."
# shellcheck disable=SC2086 # files is a list of paths with no spaces in them
gh release create "$tag" --repo "$repo" --target "$commit" --latest=false \
	--title "$title $version" --notes "$notes" $files "$sums"

echo
echo "Released $tag. Now commit site/plugins/ and merge it: the listing on the"
echo "public site is what makes these builds installable."
