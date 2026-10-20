#!/bin/sh
set -eu
tool_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
launcher=${1:-"$tool_dir/../Gateway Checks and Deploy.app"}
/usr/bin/osacompile -o "$launcher" "$tool_dir/macos/release.applescript"
/usr/bin/plutil -replace CFBundleIdentifier -string ai.sociaro.release-launcher "$launcher/Contents/Info.plist"
/usr/bin/codesign --force --sign - "$launcher"
printf '%s\n' "$launcher"
