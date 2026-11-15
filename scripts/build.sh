#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
for target in linux/amd64 linux/arm64 windows/amd64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  out="dist/${target_os}-${target_arch}"
  mkdir -p "$out"
  suffix=""
  if [[ "$target_os" == windows ]]; then suffix=".exe"; fi
  for app in coordinator edge-agent edge-cli; do
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags='-s -w' -o "$out/${app}${suffix}" "./apps/${app}/cmd/${app}"
  done
done
