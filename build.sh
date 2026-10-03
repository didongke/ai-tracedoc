#!/bin/sh
# Build the released ai-tracedoc binaries into bin/.
#
# The plugin ships prebuilt binaries so that installing it costs the user
# nothing: no Python, no Go, no toolchain, no network. The builds are pure Go
# stdlib with cgo disabled, so all six targets cross-compile from any machine
# -- one command, from Windows, Linux or macOS alike.
#
# Run from anywhere; the script cd's to the repository root itself.
set -eu

cd "$(dirname "$0")"

CGO_ENABLED=0
export CGO_ENABLED

mkdir -p bin

# The build ID names the artifact after the release it came from. Go's default
# is a content hash: it identifies the build but says nothing about which
# release it belongs to, which is the question someone running `go version -m`
# on a shipped binary is actually asking.
#
# It also changes the file hash, which is not incidental here. Windows Smart
# App Control decides per hash, and it refused the default-ID 0.3.2 build of
# tracedoc-windows-amd64.exe on the machine this was developed on, while
# accepting five of six otherwise-identical builds -- and the build is
# deterministic, so rebuilding produced the same refused file every time.
# Nothing in this script can make a binary trusted; naming the ID only means
# each release is a deliberate artifact with a hash of its own.
version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    .claude-plugin/plugin.json | head -1)
if [ -z "$version" ]; then
    echo "build.sh: cannot read the version from .claude-plugin/plugin.json" >&2
    exit 1
fi

for target in \
    windows/amd64 \
    windows/arm64 \
    linux/amd64 \
    linux/arm64 \
    darwin/amd64 \
    darwin/arm64
do
    os=${target%/*}
    arch=${target#*/}
    suffix=
    [ "$os" = windows ] && suffix=.exe
    out="bin/tracedoc-$os-$arch$suffix"
    echo "building $out"
    # -s -w drops the symbol table and DWARF data. The binaries are shipped
    # in the repository, so their size is a real cost to every clone, and
    # nothing here needs a stack trace from a release build.
    GOOS=$os GOARCH=$arch go build -trimpath \
        -ldflags="-s -w -buildid=ai-tracedoc-$version" -o "$out" ./cmd/tracedoc
done

# Set the executable bit where it means something. Git records it only if the
# filesystem has one, and Windows does not -- so a checkout there can never
# set it and git never carries it. Building on Windows leaves these 0644, and
# a Linux or macOS checkout would then be un-runnable; the README covers
# restoring the mode in git's index.
chmod +x bin/tracedoc bin/tracedoc-linux-* bin/tracedoc-darwin-* 2>/dev/null || true

echo
echo "built:"
ls -l bin/tracedoc-* | awk '{printf "  %-34s %8.2f MB\n", $NF, $5/1048576}'
