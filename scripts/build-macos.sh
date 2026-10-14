#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
arch=${1:-arm64}
case "$arch" in arm64|amd64) ;; *) echo 'Usage: scripts/build-macos.sh [arm64|amd64]' >&2; exit 2;; esac
export CGO_ENABLED=0
version=0.1.0
ts_version=v1.104.1
root=$PWD
out="$root/build/darwin-$arch"
app="$out/TailBuddy.app"
mkdir -p "$app/Contents/"{MacOS,Helpers,Resources/en.lproj,Resources/zh-Hans.lproj}
go mod download
GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "$app/Contents/MacOS/TailBuddy" ./cmd/tailbuddy
module_json="$out/tailscale-module.json"
go mod download -json "tailscale.com@$ts_version" > "$module_json"
ts_dir=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["Dir"])' "$module_json")
# Build only the upstream open-source daemon. Do not run it during packaging.
(cd "$ts_dir" && GOOS=darwin GOARCH="$arch" go build -mod=readonly -buildvcs=false -trimpath -ldflags="-s -w -X tailscale.com/version.longStamp=$ts_version -X tailscale.com/version.shortStamp=${ts_version#v}" -o "$app/Contents/Helpers/tailscaled" ./cmd/tailscaled)
python3 scripts/package-macos.py "$app" "$version"
cp assets/icon.icns "$app/Contents/Resources/TailBuddy.icns"
cp LICENSE NOTICE README.md README.zh-CN.md "$app/Contents/Resources/"
# Record exact modules and retain upstream licenses for binary redistribution.
go list -m -json all > "$out/app-modules.json"
(cd "$ts_dir" && go list -m -json all) > "$out/tailscale-modules.json"
python3 scripts/licenses.py "$app/Contents/Resources/OpenSource" "$out/app-modules.json" "$out/tailscale-modules.json"
cp "$(go env GOROOT)/LICENSE" "$app/Contents/Resources/OpenSource/Go-LICENSE"
python3 - "$module_json" "$app/Contents/Resources/OpenSource/tailscale-source.json" <<'PYMETA'
import json,sys
m=json.load(open(sys.argv[1]))
json.dump({k:m[k] for k in ('Path','Version','Sum','GoModSum','Origin') if k in m},open(sys.argv[2],'w'),indent=2)
PYMETA
if [[ $(uname -s) == Darwin ]]; then
  codesign --force --sign - "$app/Contents/Helpers/tailscaled"
  codesign --force --sign - "$app/Contents/MacOS/TailBuddy"
  codesign --force --sign - "$app"
  codesign --verify --deep --strict "$app"
  ditto -c -k --sequesterRsrc --keepParent "$app" "$out/TailBuddy-$version-macos-$arch.zip"
else
  (cd "$out" && zip -qr "TailBuddy-$version-macos-$arch.zip" TailBuddy.app)
fi
(cd "$out" && shasum -a 256 "TailBuddy-$version-macos-$arch.zip" > SHA256SUMS)
echo "Built $out/TailBuddy-$version-macos-$arch.zip"
echo 'Development build: not Developer ID signed or notarized.'
