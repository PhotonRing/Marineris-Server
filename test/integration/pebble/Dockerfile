# syntax=docker/dockerfile:1.7
# Pebble (test ACME CA) and pebble-challtestsrv (test DNS), built from a
# pinned source version. Test infrastructure only; never used in production.
FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
ARG PEBBLE_VERSION=v2.10.1
RUN --mount=type=secret,id=extra_ca,required=false \
    if [ -f /run/secrets/extra_ca ]; then export SSL_CERT_FILE=/run/secrets/extra_ca; fi; \
    CGO_ENABLED=0 go install github.com/letsencrypt/pebble/v2/cmd/pebble@${PEBBLE_VERSION} \
    && CGO_ENABLED=0 go install github.com/letsencrypt/pebble/v2/cmd/pebble-challtestsrv@${PEBBLE_VERSION}

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /go/bin/pebble /go/bin/pebble-challtestsrv /usr/local/bin/
USER 65532:65532
