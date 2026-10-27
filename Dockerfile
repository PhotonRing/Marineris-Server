# syntax=docker/dockerfile:1.7
#
# Marineris server image: a single static Go binary (with the web UI
# embedded) on distroless, running as an unprivileged user.
#
# Base images are pinned by digest; Dependabot keeps them up to date.
#
# Building behind a TLS-intercepting proxy: pass its CA with
#   --secret id=extra_ca,src=/path/to/ca.pem
# It is only used during the build and never ends up in the image.

FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:40cd011b55415bc894e45b02fdd5a5dfde056921b7d23a64c74f80497af48797 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=secret,id=extra_ca,required=false \
    if [ -f /run/secrets/extra_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/extra_ca; fi; \
    npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=secret,id=extra_ca,required=false \
    if [ -f /run/secrets/extra_ca ]; then export SSL_CERT_FILE=/run/secrets/extra_ca; fi; \
    go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/marineris ./cmd/marineris

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
LABEL org.opencontainers.image.title="Marineris" \
      org.opencontainers.image.description="Self-hosted server platform: containers, reverse proxy, certificates, SSO" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/PhotonRing/Marineris-Server" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/marineris /usr/local/bin/marineris
USER 65532:65532
EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/usr/local/bin/marineris", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/marineris"]
CMD ["serve"]
