#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
python3 "$root/scripts/check-ui-updates.py"
output=${1:-"$root/dist/gateway-ui.tar.gz"}
mkdir -p "$(dirname -- "$output")"
staging=$(mktemp -d "${TMPDIR:-/tmp}/gateway-ui.XXXXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM
cp -R "$root/web/public/." "$staging/"
cp "$root/web/Caddyfile" "$staging/Caddyfile"
cp "$root/LICENSE" "$staging/LICENSE"
cp "$root/NOTICE" "$staging/NOTICE"
# Disable macOS tar's generated AppleDouble streams, not application dotfiles.
# Other tar implementations ignore this environment variable.
COPYFILE_DISABLE=1 tar -czf "$output" -C "$staging" .
printf 'UI artifact: %s\n' "$output"
