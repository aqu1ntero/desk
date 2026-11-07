#!/usr/bin/env bash
# build.sh: desk for Windows (amd64), cross-built from Linux, into dist/:
#   desk-<version>-windows-amd64.zip  desk.exe, LICENSE.txt, README.txt (portable)
#   desk-<version>-windows-amd64.msi  installs to Program Files\desk, PATH, Start Menu shortcut
#
#   VERSION=2026.11.03 packaging/windows/build.sh
#
# Needs Go, rsvg-convert (librsvg), ImageMagick, wixl (msitools) and zip. Gio needs no cgo on
# Windows, so this runs anywhere Go does. Without VERSION the binary names its commit
# (dev-<commit>) and the MSI is 0.0.1. With SIGN_PFX (a .pfx file) and SIGN_PASSWORD, desk.exe
# and the MSI are Authenticode-signed with osslsigncode; without them they are unsigned and
# SmartScreen warns on first run.
set -euo pipefail

cd "$(dirname "$0")/../.."
VERSION="${VERSION:-}"
SIGN_PFX="${SIGN_PFX:-}"
SIGN_PASSWORD="${SIGN_PASSWORD:-}"
out=dist/windows
name="desk-${VERSION:-dev}-windows-amd64"
rm -rf "$out"
mkdir -p "$out"

# msiVersion maps a dated version onto MSI's major.minor.build (≤255.≤255.≤65535; Windows
# Installer ignores a fourth field when it compares): YY.MM.(DD*100+N), so 2026.11.05 is
# 26.11.500 and 2026.11.05.1 26.11.501. A nightly (2026.11.07~git1a2b3c4) gets its day's
# number; the same day's release replaces it (AllowSameVersionUpgrades in desk.wxs).
msiVersion() {
	if [[ "${1%%~*}" =~ ^20([0-9]{2})\.([0-9]{2})\.([0-9]{2})(\.([0-9]+))?$ ]]; then
		echo "$((10#${BASH_REMATCH[1]})).$((10#${BASH_REMATCH[2]})).$((10#${BASH_REMATCH[3]} * 100 + ${BASH_REMATCH[5]:-0}))"
	else
		echo 0.0.1
	fi
}
msiver=$(msiVersion "$VERSION")
# fileVersion is the exe's version resource: four numbers, 2026.11.5.1
fileVersion() {
	if [[ "${1%%~*}" =~ ^([0-9]{4})\.([0-9]{2})\.([0-9]{2})(\.([0-9]+))?$ ]]; then
		echo "${BASH_REMATCH[1]}.$((10#${BASH_REMATCH[2]})).$((10#${BASH_REMATCH[3]})).${BASH_REMATCH[5]:-0}"
	else
		echo 0.0.0.0
	fi
}

# the icon: rsvg-convert rasterizes each size, ImageMagick packs them into one .ico
im=$(command -v magick || command -v convert)
pngs=()
for s in 16 24 32 48 64 128 256; do
	rsvg-convert -w $s -h $s packaging/desk.svg -o "$out/icon-$s.png"
	pngs+=("$out/icon-$s.png")
done
"$im" "${pngs[@]}" "$out/desk.ico"
rm -f "${pngs[@]}"

# desk.exe's icon and version info as a .syso next to main, which go build links in; removed
# after the build so the tree stays clean
syso=cmd/desk/rsrc_windows_amd64.syso
trap 'rm -f "$syso"' EXIT
GOARCH=amd64 go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --out cmd/desk/rsrc \
	--icon "$out/desk.ico" --manifest gui --product-name desk --file-description desk \
	--original-filename desk.exe --copyright "MIT License" \
	--file-version "$(fileVersion "$VERSION")" --product-version "${VERSION:-dev}"

# -H windowsgui: no console window behind desk's own when it is started from the Start Menu
ldflags="-s -w -H windowsgui"
[[ -n "$VERSION" ]] && ldflags+=" -X main.version=$VERSION"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags nox11 -trimpath -ldflags "$ldflags" \
	-o "$out/desk.exe" ./cmd/desk

# sign signs $1 in place (Authenticode, SHA-256, timestamped); only with SIGN_PFX
sign() {
	[[ -n "$SIGN_PFX" ]] || return 0
	osslsigncode sign -pkcs12 "$SIGN_PFX" -pass "$SIGN_PASSWORD" -h sha256 -n desk \
		-i https://github.com/lfdominguez/desk -t http://timestamp.digicert.com -in "$1" -out "$1.signed"
	mv "$1.signed" "$1"
}
sign "$out/desk.exe"

# portable zip
zipdir="$out/zip"
mkdir -p "$zipdir"
cp "$out/desk.exe" "$zipdir/"
cp LICENSE "$zipdir/LICENSE.txt"
cp packaging/windows/README.txt "$zipdir/README.txt"
rm -f "dist/$name.zip"
(cd "$zipdir" && zip -q -9 "../../$name.zip" desk.exe LICENSE.txt README.txt)

# MSI
wixl -a x64 -D Version="$msiver" -D Exe="$out/desk.exe" -D License="$zipdir/LICENSE.txt" \
	-D Icon="$out/desk.ico" -o "dist/$name.msi" packaging/windows/desk.wxs
sign "dist/$name.msi"

echo "dist/$name.zip"
echo "dist/$name.msi (ProductVersion $msiver)"
