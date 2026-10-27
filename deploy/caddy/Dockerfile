# syntax=docker/dockerfile:1.7
#
# Caddy built with the modules Marineris uses:
#   - DNS-01 providers: OVH, Cloudflare, Gandi, Infomaniak, Route 53
#   - CrowdSec bouncer, rate limiting
# It runs as an unprivileged user; binding 80/443 is allowed through the
# net.ipv4.ip_unprivileged_port_start sysctl set in compose.yml.

ARG CADDY_VERSION=v2.11.7

# The build stage runs on the build machine's architecture and cross-compiles
# Caddy for the target one: no CPU emulation, so multi-arch builds stay fast.
FROM --platform=$BUILDPLATFORM caddy:2.11-builder-alpine@sha256:ef9b65adf9a224770c6d5f6d1ae1bf99bc3caaa78d8c4bae3ea949ee6984f301 AS build
ARG CADDY_VERSION
ARG TARGETOS
ARG TARGETARCH
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
# The binary must not carry file capabilities: the container drops all
# capabilities, and such a binary would then fail to execute. xcaddy only
# adds them when XCADDY_SETCAP=1.
ENV XCADDY_SETCAP=0
# crowdsec and x/net are pinned above the versions the plugins require, to
# pick up security fixes (CVE-2026-44982, CVE-2026-78669).
# The module check runs the binary, so it is done for the native build only
# (the other architectures are built from the same module list).
RUN --mount=type=secret,id=extra_ca,required=false \
    set -e; \
    if [ -f /run/secrets/extra_ca ]; then export SSL_CERT_FILE=/run/secrets/extra_ca; fi; \
    GOOS=$TARGETOS GOARCH=$TARGETARCH xcaddy build ${CADDY_VERSION} \
      --output /out/caddy \
      --with github.com/caddy-dns/ovh@v1.1.0 \
      --with github.com/caddy-dns/cloudflare@v0.2.4 \
      --with github.com/caddy-dns/gandi@v1.2.1 \
      --with github.com/caddy-dns/infomaniak@v1.0.2 \
      --with github.com/caddy-dns/route53@v1.6.2 \
      --with github.com/hslatman/caddy-crowdsec-bouncer/http@v0.14.2-0.20260909201224-ffdcb7c6f861 \
      --with github.com/mholt/caddy-ratelimit@v0.1.0 \
      --replace github.com/crowdsecurity/crowdsec=github.com/crowdsecurity/crowdsec@v1.7.8 \
      --replace golang.org/x/net=golang.org/x/net@v0.61.0; \
    if [ "$TARGETARCH" = "$(go env GOARCH)" ]; then \
      for m in dns.providers.ovh dns.providers.cloudflare dns.providers.gandi dns.providers.infomaniak \
               dns.providers.route53 http.handlers.crowdsec http.handlers.rate_limit; do \
        /out/caddy list-modules | grep -qx "$m" || { echo "missing module $m"; exit 1; }; \
      done; \
    fi

FROM caddy:2.11-alpine@sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f
COPY --from=build /out/caddy /usr/bin/caddy
COPY bootstrap.json /etc/caddy/bootstrap.json
# Security updates of the base image; curl is not used.
# hadolint ignore=DL3017
RUN apk upgrade --no-cache && apk del --no-cache curl \
    && mkdir -p /run/caddy-admin /var/log/caddy /data /config /srv/static \
    && chown -R 65532:65532 /run/caddy-admin /var/log/caddy /data /config \
    && chmod 0750 /run/caddy-admin /var/log/caddy
LABEL org.opencontainers.image.title="Marineris Caddy" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/PhotonRing/Marineris-Server"
USER 65532:65532
ENV XDG_CONFIG_HOME=/config XDG_DATA_HOME=/data
# --resume restores the last configuration pushed by Marineris; the bootstrap
# file is only used on the very first start.
CMD ["caddy", "run", "--resume", "--config", "/etc/caddy/bootstrap.json"]
