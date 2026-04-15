#!/usr/bin/env bash
#
# Build Seismo.app — single-bundle macOS 13+ menu bar app that embeds the Go
# seismo binary as a LaunchDaemon registered via SMAppService.
#
# Output: ./Seismo.app  (drag into /Applications, launch, approve helper)
#
set -euo pipefail
cd "$(dirname "$0")"

APP_NAME="Seismo"
BUNDLE_ID="com.gojaehyeon.seismo"
HELPER_LABEL="com.gojaehyeon.seismo.helper"
APP_DIR="${APP_NAME}.app"
CONTENTS="${APP_DIR}/Contents"
SIGN_IDENTITY="${SIGN_IDENTITY:--}"

if ! command -v swiftc >/dev/null; then
  echo "error: swiftc not found. install Xcode CLT: xcode-select --install" >&2
  exit 1
fi
if ! command -v go >/dev/null; then
  echo "error: go not found. install from https://go.dev/dl/" >&2
  exit 1
fi

echo "==> cleaning"
rm -rf "${APP_DIR}"
mkdir -p "${CONTENTS}/MacOS" \
         "${CONTENTS}/Resources" \
         "${CONTENTS}/Library/LaunchDaemons"

echo "==> building Go seismo-helper"
( cd .. && go build -o "app/${CONTENTS}/MacOS/seismo-helper" ./cmd/seismo )

echo "==> compiling Swift menu bar app"
swiftc \
  -O \
  -target arm64-apple-macos13 \
  -framework Cocoa \
  -framework ServiceManagement \
  -o "${CONTENTS}/MacOS/${APP_NAME}" \
  SeismoApp.swift

echo "==> writing Info.plist"
cat > "${CONTENTS}/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>          <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>          <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>                <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>         <string>Seismo</string>
    <key>CFBundlePackageType</key>         <string>APPL</string>
    <key>CFBundleVersion</key>             <string>0.1</string>
    <key>CFBundleShortVersionString</key>  <string>0.1</string>
    <key>LSMinimumSystemVersion</key>      <string>13.0</string>
    <key>LSUIElement</key>                 <true/>
    <key>NSHighResolutionCapable</key>     <true/>
    <key>NSAppTransportSecurity</key>
    <dict>
        <key>NSAllowsLocalNetworking</key> <true/>
    </dict>
</dict>
</plist>
PLIST

echo "==> writing bundled LaunchDaemon plist"
cat > "${CONTENTS}/Library/LaunchDaemons/${HELPER_LABEL}.plist" <<HPLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${HELPER_LABEL}</string>

    <key>BundleProgram</key>
    <string>Contents/MacOS/seismo-helper</string>

    <key>ProgramArguments</key>
    <array>
        <string>Contents/MacOS/seismo-helper</string>
        <string>-addr</string>
        <string>127.0.0.1:8766</string>
    </array>

    <key>RunAtLoad</key>  <true/>
    <key>KeepAlive</key>  <true/>

    <key>StandardOutPath</key>
    <string>/tmp/seismo-helper.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/seismo-helper.err</string>
</dict>
</plist>
HPLIST

echo "==> code-signing bundle"
codesign --force --sign "${SIGN_IDENTITY}" "${CONTENTS}/MacOS/seismo-helper"
codesign --force --sign "${SIGN_IDENTITY}" "${CONTENTS}/MacOS/${APP_NAME}"
codesign --force --deep --sign "${SIGN_IDENTITY}" "${APP_DIR}"

if [[ "${SIGN_IDENTITY}" == "-" ]]; then
  echo "warning: built with ad-hoc signing only."
  echo "         For distribution, rebuild with SIGN_IDENTITY='Developer ID Application: ...' and notarize the app."
fi

echo ""
echo "✓ built ${APP_DIR}"
ls -lh "${CONTENTS}/MacOS/"
echo ""
cat <<INSTRUCTIONS
install:
  1) cp -R ${APP_DIR} /Applications/
  2) open /Applications/${APP_DIR}
  3) menu bar shows ◇ — click → 'enable helper…'
  4) approve in System Settings → Login Items & Extensions if prompted
  5) menu bar flips to ◆ 0.000g  — live PGA updates next to the icon
INSTRUCTIONS
