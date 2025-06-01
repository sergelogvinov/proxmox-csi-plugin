# syntax = docker/dockerfile:1.12
########################################

FROM golang:1.24-bookworm AS develop

WORKDIR /src
COPY ["go.mod", "go.sum", "/src"]
RUN go mod download

########################################

FROM --platform=${BUILDPLATFORM} golang:1.24.3-alpine3.21 AS builder
RUN apk update && apk add --no-cache make
ENV GO111MODULE=on
WORKDIR /src

COPY ["go.mod", "go.sum", "/src"]
RUN go mod download && go mod verify

COPY . .
ARG TAG
ARG SHA
RUN make build-all-archs

########################################

FROM --platform=${TARGETARCH} scratch AS proxmox-csi-controller
LABEL org.opencontainers.image.source="https://github.com/sergelogvinov/proxmox-csi-plugin" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Proxmox VE CSI plugin"

COPY --from=gcr.io/distroless/static-debian12:nonroot . .
ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-controller-${TARGETARCH} /bin/proxmox-csi-controller

ENTRYPOINT ["/bin/proxmox-csi-controller"]

########################################

FROM --platform=${TARGETARCH} debian:12.10 AS tools

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    mount \
    udev \
    e2fsprogs \
    xfsprogs \
    util-linux \
    cryptsetup \
    rsync

COPY tools /tools
RUN /tools/deps.sh

########################################

FROM --platform=${TARGETARCH} gcr.io/distroless/base-debian12 AS tools-check

COPY --from=tools /bin/sh /bin/sh
COPY --from=tools /tools /tools
COPY --from=tools /dest /

SHELL ["/bin/sh"]
RUN /tools/deps-check.sh

########################################

FROM --platform=${TARGETARCH} scratch AS proxmox-csi-node
LABEL org.opencontainers.image.source="https://github.com/sergelogvinov/proxmox-csi-plugin" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Proxmox VE CSI plugin"

COPY --from=gcr.io/distroless/base-debian12 . .
COPY --from=tools /dest /

ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-node-${TARGETARCH} /bin/proxmox-csi-node

ENTRYPOINT ["/bin/proxmox-csi-node"]

########################################

FROM alpine:3.22 AS pvecsictl
LABEL org.opencontainers.image.source="https://github.com/sergelogvinov/proxmox-csi-plugin" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Proxmox VE CSI tools"

ARG TARGETARCH
COPY --from=builder /src/bin/pvecsictl-${TARGETARCH} /bin/pvecsictl

ENTRYPOINT ["/bin/pvecsictl"]

########################################

FROM alpine:3.22 AS pvecsictl-goreleaser
LABEL org.opencontainers.image.source="https://github.com/sergelogvinov/proxmox-csi-plugin" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Proxmox VE CSI tools"

ARG TARGETARCH
COPY pvecsictl-linux-${TARGETARCH} /bin/pvecsictl

ENTRYPOINT ["/bin/pvecsictl"]
