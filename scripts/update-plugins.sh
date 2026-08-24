#!/bin/bash
# Downloads the given session-manager-plugin version for all five embedded
# platforms and replaces the binaries under internal/assets/plugin/.
# Requires: curl, unzip, cpio, bsdtar (libarchive-tools).
set -euo pipefail

version=${1:?usage: $0 <plugin-version>}
base="https://s3.amazonaws.com/session-manager-downloads/plugin/${version}"

root="$(cd "$(dirname "$0")/.." && pwd)"
dest="$root/internal/assets/plugin"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Updating embedded session-manager-plugin to ${version}"

# Linux: extract the binary from the .deb packages (AWS publishes no raw
# Linux binaries)
for arch in 64bit arm64; do
    out="linux_amd64"
    [ "$arch" = "arm64" ] && out="linux_arm64"

    mkdir -p "$tmp/$out"
    (
        cd "$tmp/$out"
        curl -sfo plugin.deb "$base/ubuntu_${arch}/session-manager-plugin.deb"
        bsdtar -xf plugin.deb
        bsdtar -xf data.tar.*
        cp usr/local/sessionmanagerplugin/bin/session-manager-plugin "$dest/$out/session-manager-plugin"
        chmod 755 "$dest/$out/session-manager-plugin"
    )
    echo "  $out: ok"
done

# Windows: a zip containing package.zip containing bin/session-manager-plugin.exe
curl -sfo "$tmp/win.zip" "$base/windows/SessionManagerPlugin.zip"
unzip -qo "$tmp/win.zip" -d "$tmp/win"
unzip -qo "$tmp/win/package.zip" -d "$tmp/winpkg"
cp "$tmp/winpkg/bin/session-manager-plugin.exe" "$dest/windows_amd64/session-manager-plugin.exe"
echo "  windows_amd64: ok"

# macOS: a .pkg (xar archive) whose Payload is a gzipped cpio.
# Intel packages live under mac/, Apple Silicon under mac_arm64/.
for dir in mac mac_arm64; do
    out="darwin_amd64"
    [ "$dir" = "mac_arm64" ] && out="darwin_arm64"

    mkdir -p "$tmp/$out"
    (
        cd "$tmp/$out"
        curl -sfo plugin.pkg "$base/${dir}/session-manager-plugin.pkg"
        # libarchive extracts xar entries with doubled names (PayloadPayload),
        # so match on the prefix
        bsdtar -xf plugin.pkg
        payload=$(find . -type f -name 'Payload*' | head -1)
        [ -n "$payload" ] || { echo "no Payload found in ${dir} pkg" >&2; exit 1; }
        # the payload is a cpio archive (plain or compressed); bsdtar
        # auto-detects either way
        bsdtar -xf "$payload"
        bin=$(find . -type f -name session-manager-plugin -path '*bin*' | head -1)
        [ -n "$bin" ] || { echo "no plugin binary found in ${dir} pkg" >&2; exit 1; }
        cp "$bin" "$dest/$out/session-manager-plugin"
        chmod 755 "$dest/$out/session-manager-plugin"
    )
    echo "  $out: ok"
done

echo "$version" > "$dest/VERSION"
echo "Done: embedded plugins are now ${version}"
