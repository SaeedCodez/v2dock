#!/bin/sh
# Build v2dock:local without pulling the Go/Node base images:
# UI and binary are built on this machine, then packaged on alpine.
set -eu
cd "$(dirname "$0")/.."
ARCH=$(docker version --format '{{.Server.Arch}}')
(cd ui && npm run build >/dev/null)
mkdir -p dev/prebuilt/ctx
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags "-s -w -X main.version=${VERSION:-local}" -o dev/prebuilt/ctx/v2dock ./cmd/v2dock
cp dev/assets/geoip.dat dev/assets/geosite.dat dev/prebuilt/ctx/
docker build -q -t v2dock:local -f dev/prebuilt/Dockerfile dev/prebuilt/ctx
echo "built v2dock:local ($ARCH)"
