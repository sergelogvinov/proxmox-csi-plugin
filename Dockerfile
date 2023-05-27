# syntax = docker/dockerfile:1.4
########################################

FROM golang:1.20-buster AS develop

WORKDIR /src
COPY ["go.mod", "go.sum", "/src"]
RUN go mod download

########################################

FROM --platform=${BUILDPLATFORM} golang:1.20.4-alpine3.18 AS builder
RUN apk update && apk add --no-cache make
ENV GO111MODULE on
WORKDIR /src

COPY ["go.mod", "go.sum", "/src"]
RUN go mod download && go mod verify

COPY . .
ARG TAG
ARG SHA
RUN make build-all-archs

########################################

FROM --platform=${TARGETARCH} scratch AS controller
LABEL org.opencontainers.image.source="https://github.com/sergelogvinov/proxmox-csi-plugin" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Proxmox VE CSI plugin"

COPY --from=gcr.io/distroless/static-debian11:nonroot . .
ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-controller-${TARGETARCH} /bin/proxmox-csi-controller

ENTRYPOINT ["/bin/proxmox-csi-controller"]

########################################

FROM --platform=${TARGETARCH} registry.k8s.io/build-image/debian-base:bullseye-v1.4.3 AS tools

RUN clean-install \
    bash \
    mount \
    udev \
    e2fsprogs \
    xfsprogs \
    rsync

COPY tools /tools
RUN /tools/deps.sh

########################################

FROM --platform=${TARGETARCH} gcr.io/distroless/base-debian11 AS tools-check

COPY --from=tools /bin/sh /bin/sh
COPY --from=tools /tools /tools
COPY --from=tools /dest /

SHELL ["/bin/sh"]
RUN /tools/deps-check.sh

########################################

FROM --platform=${TARGETARCH} scratch AS node
LABEL org.opencontainers.image.source="https://github.com/sergelogvinov/proxmox-csi-plugin" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Proxmox VE CSI plugin"

COPY --from=gcr.io/distroless/base-debian11 . .
COPY --from=tools /dest /

ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-node-${TARGETARCH} /bin/proxmox-csi-node

ENTRYPOINT ["/bin/proxmox-csi-node"]
