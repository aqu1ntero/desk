#!/usr/bin/env bash
# build-app.sh: dist/desk.app (a universal arm64 + x86_64 binary) and dist/desk-<version>-macos.zip,
# ad-hoc signed. Run on a Mac with Go and the Xcode command line tools, from anywhere:
#
#   VERSION=2026.11.03 packaging/macos/build-app.sh
#
# Without VERSION the binary names its commit (dev-<commit>), as a plain go build does.
# ARCHS="arm64" builds one architecture only. SIGN_IDENTITY="Developer ID Application: …" signs
# with a real certificate (hardened runtime, timestamp) in place of the ad-hoc signature.
# The bundle matters beyond looks: notifications (gioui.org/x/notify) need a bundle identifier,
# so a bare binary runs but never notifies.
set -euo pipefail

cd "$(dirname "$0")/../.."
VERSION="${VERSION:-}"
ARCHS="${ARCHS:-arm64 amd64}"
SIGN_IDENTITY="${SIGN_IDENTITY:--}"
BUNDLE_ID=io.github.lfdominguez.desk
# Go 1.26 runs on macOS 12 and later; without this clang stamps the build machine's version
export MACOSX_DEPLOYMENT_TARGET=12.0

dist=dist
app=$dist/desk.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

ldflags="-s -w"
[[ -n "$VERSION" ]] && ldflags+=" -X main.version=$VERSION"
bins=()
for arch in $ARCHS; do
	carch=$arch
	[[ $arch == amd64 ]] && carch=x86_64
	CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $carch" \
		go build -tags nox11 -trimpath -ldflags "$ldflags" -o "$dist/desk-$arch" ./cmd/desk
	bins+=("$dist/desk-$arch")
done
lipo -create -output "$app/Contents/MacOS/desk" "${bins[@]}"
rm -f "${bins[@]}"

# the icon: sips rasterizes the SVG itself at each size (Quick Look would add a white background)
iconset=$dist/desk.iconset
rm -rf "$iconset"
mkdir -p "$iconset"
for s in 16 32 128 256 512; do
	sips -s format png -z $s $s packaging/desk.svg --out "$iconset/icon_${s}x${s}.png" >/dev/null
	sips -s format png -z $((s * 2)) $((s * 2)) packaging/desk.svg --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns -o "$app/Contents/Resources/desk.icns" "$iconset"
rm -rf "$iconset"

# CFBundleShortVersionString wants numbers and dots: a nightly's 2026.11.07~git1a2b3c4 gives 2026.11.07
short="${VERSION%%~*}"
[[ "$short" =~ ^[0-9.]+$ ]] || short=0.0.0
cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>desk</string>
	<key>CFBundleDisplayName</key><string>desk</string>
	<key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
	<key>CFBundleExecutable</key><string>desk</string>
	<key>CFBundleIconFile</key><string>desk</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$short</string>
	<key>CFBundleVersion</key><string>${VERSION:-$short}</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
	<key>LSMinimumSystemVersion</key><string>$MACOSX_DEPLOYMENT_TARGET</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSHumanReadableCopyright</key><string>MIT License</string>
</dict>
</plist>
EOF
plutil -lint "$app/Contents/Info.plist" >/dev/null

if [[ "$SIGN_IDENTITY" == - ]]; then
	codesign --force --sign - "$app"
else
	codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$app"
fi
codesign --verify --strict "$app"

zip="$dist/desk-${VERSION:-dev}-macos.zip"
rm -f "$zip"
ditto -c -k --keepParent "$app" "$zip"
echo "$zip"
