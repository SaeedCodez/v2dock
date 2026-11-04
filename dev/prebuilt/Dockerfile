# Development-only image: packages a binary cross-compiled on the host
# (dev/build-local.sh) so a local test does not need the Go and Node base
# images. Releases use the multi-stage Dockerfile at the repository root.
FROM alpine:3.22
RUN apk add --no-cache nftables iproute2 iptables iptables-legacy ca-certificates \
 && mkdir -p /data /run/v2dock/agent /run/v2dock/docker && chmod 700 /data
COPY v2dock /usr/local/bin/v2dock
COPY geoip.dat geosite.dat /usr/share/v2dock/
ENV V2DOCK_DATA=/data V2DOCK_ASSETS=/usr/share/v2dock XRAY_LOCATION_ASSET=/usr/share/v2dock XTABLES_LOCKFILE=/tmp/xtables.lock
EXPOSE 8080
ENTRYPOINT ["v2dock"]
CMD ["web"]
