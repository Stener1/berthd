#!/bin/sh
# Build Berth's own tmux for Linux boxes: one static binary per architecture
# (musl, with libevent and ncurses linked in), which `berth add ssh` uploads
# to ~/.local/bin/tmux on a box that has no tmux, so the most common thing a
# fresh box lacks needs no sudo.
#
#   scripts/build-tmux.sh [OUTDIR] [ARCH...]    (default: bin, amd64 arm64)
#
# Every source is pinned to a release and checked against its sha256 before
# it is built, in an Alpine container pinned by digest, with zig cc
# cross-compiling for each architecture, so the same inputs give the same
# binary and nothing runs under emulation. It needs Docker (OrbStack, Docker
# Desktop, or Docker on Linux). A binary already in OUTDIR is kept;
# delete it to build again. make daemons runs this when Docker is there and
# says how to get by without it when it isn't.
#
# tmux is ISC licensed, libevent 3-clause BSD, ncurses MIT (X11); see
# THIRD_PARTY_NOTICES.md.
set -eu

out=${1:-bin}
shift 2>/dev/null || true
archs=${*:-amd64 arm64}

TMUX_VERSION=3.7c
TMUX_SHA256=7c60cae9a0e25288e2e24750aafc9e8800fc7fd4555e447e1b29ee4201cfb3bf
LIBEVENT_VERSION=2.1.12-stable
LIBEVENT_SHA256=92e6de1be9ec176428fd2367677e61ceffc2ee1cb119035037a27d346b0403bb
NCURSES_VERSION=6.5
NCURSES_SHA256=136d91bc269a9a5785e5f9e980bc76ab57428f604ce3e5a5a90cebc767971cc6
# alpine:3.22, the multi-architecture index.
ALPINE=alpine@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

command -v docker >/dev/null 2>&1 || {
	echo "build-tmux: needs Docker to build Berth's tmux; without it, berth add ssh installs tmux with the box's package manager instead" >&2
	exit 2
}
mkdir -p "$out"
out=$(cd "$out" && pwd)
src="$out/.tmux-src"
mkdir -p "$src"

# fetch NAME SHA256 URL...: the first URL that answers, checked.
fetch() {
	name=$1 sum=$2
	shift 2
	if [ -f "$src/$name" ] && check "$src/$name" "$sum"; then return; fi
	for url in "$@"; do
		if curl -fsSL --retry 3 --max-time 300 -o "$src/$name.part" "$url"; then
			if check "$src/$name.part" "$sum"; then
				mv "$src/$name.part" "$src/$name"
				return
			fi
			echo "build-tmux: $url does not match its sha256" >&2
		fi
	done
	rm -f "$src/$name.part"
	echo "build-tmux: could not download $name" >&2
	exit 1
}
check() {
	if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$1" | cut -d' ' -f1); else got=$(shasum -a 256 "$1" | cut -d' ' -f1); fi
	[ "$got" = "$2" ]
}

fetch "tmux-$TMUX_VERSION.tar.gz" "$TMUX_SHA256" \
	"https://github.com/tmux/tmux/releases/download/$TMUX_VERSION/tmux-$TMUX_VERSION.tar.gz"
fetch "libevent-$LIBEVENT_VERSION.tar.gz" "$LIBEVENT_SHA256" \
	"https://github.com/libevent/libevent/releases/download/release-$LIBEVENT_VERSION/libevent-$LIBEVENT_VERSION.tar.gz"
fetch "ncurses-$NCURSES_VERSION.tar.gz" "$NCURSES_SHA256" \
	"https://invisible-mirror.net/archives/ncurses/ncurses-$NCURSES_VERSION.tar.gz" \
	"https://ftp.gnu.org/gnu/ncurses/ncurses-$NCURSES_VERSION.tar.gz"

for arch in $archs; do
	case $arch in amd64 | arm64) ;; *)
		echo "build-tmux: $arch is not amd64 or arm64" >&2
		exit 1
		;;
	esac
	dest="$out/tmux-linux-$arch"
	if [ -x "$dest" ]; then
		echo "tmux-linux-$arch: already built ($dest)"
		continue
	fi
	echo "tmux-linux-$arch: building tmux $TMUX_VERSION (static, musl)"
	# One container of this machine's own architecture builds either: zig cc
	# cross-compiles, so nothing runs under emulation.
	docker run --rm -v "$src:/src:ro" -v "$out:/out" \
		-e TMUX_VERSION="$TMUX_VERSION" -e LIBEVENT_VERSION="$LIBEVENT_VERSION" -e NCURSES_VERSION="$NCURSES_VERSION" \
		-e ARCH="$arch" -e OWNER="$(id -u):$(id -g)" "$ALPINE" sh -euc '
		apk add --no-cache build-base zig bison >/dev/null
		case $ARCH in amd64) t=x86_64-linux-musl ;; arm64) t=aarch64-linux-musl ;; esac
		export CC="zig cc -target $t" AR="zig ar" RANLIB="zig ranlib"
		export ZIG_GLOBAL_CACHE_DIR=/tmp/zig-cache ZIG_LOCAL_CACHE_DIR=/tmp/zig-cache
		p=/opt/static
		log=/tmp/build.log
		run() { "$@" >>$log 2>&1 || { tail -40 $log >&2; exit 1; }; }
		cd /tmp
		tar -xzf /src/libevent-$LIBEVENT_VERSION.tar.gz
		cd libevent-$LIBEVENT_VERSION
		run ./configure --host=$t --prefix=$p --enable-static --disable-shared --disable-openssl --disable-samples --disable-libevent-regress --disable-debug-mode
		run make -j"$(nproc)"
		run make install
		cd /tmp
		tar -xzf /src/ncurses-$NCURSES_VERSION.tar.gz
		cd ncurses-$NCURSES_VERSION
		# terminfo where every distribution keeps it (Debian and Ubuntu in
		# /lib and /usr/share, Fedora and Arch in /usr/share).
		run ./configure --host=$t --with-build-cc=gcc --prefix=$p --without-shared --with-normal --without-debug \
			--without-cxx --without-cxx-binding --without-ada --without-manpages --without-tests --without-progs \
			--enable-widec --disable-db-install --disable-stripping \
			--with-terminfo-dirs=/etc/terminfo:/lib/terminfo:/usr/share/terminfo:/usr/lib/terminfo \
			--with-default-terminfo-dir=/usr/share/terminfo
		run make -j"$(nproc)"
		run make install
		cd /tmp
		tar -xzf /src/tmux-$TMUX_VERSION.tar.gz
		cd tmux-$TMUX_VERSION
		run ./configure --host=$t --enable-static --prefix=/usr \
			CFLAGS="-O2 -I$p/include -I$p/include/ncursesw" LDFLAGS="-s -L$p/lib" \
			LIBEVENT_CFLAGS="-I$p/include" LIBEVENT_LIBS="-L$p/lib -levent_core" \
			LIBEVENT_CORE_CFLAGS="-I$p/include" LIBEVENT_CORE_LIBS="-L$p/lib -levent_core" \
			LIBNCURSES_CFLAGS="-I$p/include/ncursesw" LIBNCURSES_LIBS="-L$p/lib -lncursesw" \
			LIBTINFO_CFLAGS="-I$p/include/ncursesw" LIBTINFO_LIBS="-L$p/lib -lncursesw"
		run make -j"$(nproc)"
		if readelf -d tmux 2>/dev/null | grep -q NEEDED; then echo "tmux is not static" >&2; exit 1; fi
		readelf -h tmux | grep -E "Machine"
		cp tmux /out/tmux-linux-$ARCH.new
		chown "$OWNER" /out/tmux-linux-$ARCH.new
	'
	mv "$dest.new" "$dest"
	chmod 755 "$dest"
	echo "tmux-linux-$arch: $(du -h "$dest" | cut -f1)"
done
