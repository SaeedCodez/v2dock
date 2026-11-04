# syntax=docker/dockerfile:1
#
# One image, three roles (see docker-compose.yml):
#   v2dock guard   read-only Docker API filter      (no network, no capabilities)
#   v2dock agent   Xray + nftables transparent proxy (host network, NET_ADMIN only)
#   v2dock web     UI / API gateway                  (unprivileged user, no capabilities)

FROM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/v2dock ./cmd/v2dock

# Iran routing data: geoip:ir / geoip:private / geosite:ir from
# github.com/chocolate4u/Iran-v2ray-rules ("lite" files: only what v2dock uses).
# Checksums are verified against the release's published sha256 files.
FROM alpine:3.22 AS geodata
ARG GEO_URL=https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download
RUN apk add --no-cache curl \
 && mkdir /geo && cd /geo \
 && for f in geoip-lite.dat geosite-lite.dat; do \
      curl -fsSL --retry 3 -o "$f" "$GEO_URL/$f" \
      && curl -fsSL --retry 3 -o "$f.sha256sum" "$GEO_URL/$f.sha256sum" \
      && echo "$(cut -d' ' -f1 "$f.sha256sum")  $f" | sha256sum -c - ; \
    done \
 && mv geoip-lite.dat geoip.dat && mv geosite-lite.dat geosite.dat && rm -f ./*.sha256sum

FROM alpine:3.22
# nftables + iproute2 install the transparent-proxy rules; iptables (nft and
# legacy backends) is only used to add one ACCEPT rule on hosts whose INPUT
# policy drops traffic (UFW).
RUN apk add --no-cache nftables iproute2 iptables iptables-legacy ca-certificates \
 && mkdir -p /data /run/v2dock/agent /run/v2dock/docker \
 && chmod 700 /data
COPY --from=build /out/v2dock /usr/local/bin/v2dock
COPY --from=geodata /geo/ /usr/share/v2dock/
ENV V2DOCK_DATA=/data \
    V2DOCK_ASSETS=/usr/share/v2dock \
    XRAY_LOCATION_ASSET=/usr/share/v2dock \
    XTABLES_LOCKFILE=/tmp/xtables.lock
LABEL org.opencontainers.image.source="https://github.com/SaeedCodez/v2dock" \
      org.opencontainers.image.description="Route Docker containers through V2Ray/Xray to keep servers online during internet shutdowns" \
      org.opencontainers.image.licenses="MIT"
EXPOSE 8080
ENTRYPOINT ["v2dock"]
CMD ["web"]
