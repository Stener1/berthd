#!/bin/sh
# Build the CLI and daemon archives a release carries, into dist/:
#
#   berthd-<os>-<arch>.tar.gz   berthd, for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64
#   berth-<os>-<arch>.tar.gz    berth, plus the Linux daemons `berth add ssh` and
#                               `berth upgrade` upload, and Berth's static tmux
#                               for Linux boxes (tmux-linux-amd64, -arm64), for
#                               the same four platforms
#   checksums.txt               sha256 of every archive, as sha256sum prints it
#
#   scripts/build-release.sh v1.2.3     (make release VERSION=1.2.3 runs this)
#
# The names carry no version, so https://github.com/…/releases/latest/download/NAME
# always finds the newest; the version is stamped into each binary instead
# (`berthd version`). site/install.sh downloads these.
set -eu

version=${1:?usage: scripts/build-release.sh vX.Y.Z}
case "$version" in
v*) ;;
dev) ;;
*) version="v$version" ;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
dist="$root/dist"
go=${GO:-go}
ldflags="-s -w -X github.com/sean-brydon/berthd/internal/version.Version=$version"
platforms="linux/amd64 linux/arm64 darwin/arm64 darwin/amd64"

rm -rf "$dist"
mkdir -p "$dist/stage"
cd "$root"

# Berth's tmux, built once into bin/ (scripts/build-tmux.sh, which needs
# Docker), or put there by the release workflow's tmux job.
tmux_dir=${TMUX_DIR:-$root/bin}
if [ ! -x "$tmux_dir/tmux-linux-amd64" ] || [ ! -x "$tmux_dir/tmux-linux-arm64" ]; then
	scripts/build-tmux.sh "$tmux_dir"
fi

build() { # build PACKAGE OS ARCH OUT
	CGO_ENABLED=0 GOOS=$2 GOARCH=$3 "$go" build -trimpath -ldflags "$ldflags" -o "$4" "$1"
}

for p in $platforms; do
	os=${p%/*} arch=${p#*/}
	echo "berthd $version $os/$arch"
	build ./cmd/berthd "$os" "$arch" "$dist/stage/berthd-$os-$arch/berthd"
done
for p in $platforms; do
	os=${p%/*} arch=${p#*/}
	echo "berth $version $os/$arch"
	dir="$dist/stage/berth-$os-$arch"
	build ./cmd/berth "$os" "$arch" "$dir/berth"
	for a in amd64 arm64; do
		cp "$dist/stage/berthd-linux-$a/berthd" "$dir/berthd-linux-$a"
		cp "$tmux_dir/tmux-linux-$a" "$dir/tmux-linux-$a"
	done
done

# gzip -n leaves the time out of the archive; the files inside keep theirs.
# macOS's tar would add extended attributes GNU tar warns about.
tarflags=
if tar --version 2>/dev/null | grep -q bsdtar; then
	tarflags="--no-mac-metadata --no-xattrs"
fi
for dir in "$dist"/stage/*; do
	name=$(basename "$dir")
	# shellcheck disable=SC2086 # tarflags is a list of flags, or nothing
	(cd "$dir" && COPYFILE_DISABLE=1 tar $tarflags -cf - -- *) | gzip -9n >"$dist/$name.tar.gz"
done
rm -rf "$dist/stage"

cd "$dist"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum -- *.tar.gz >checksums.txt
else
	shasum -a 256 -- *.tar.gz >checksums.txt
fi
cat checksums.txt
