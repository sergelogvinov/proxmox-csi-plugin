# syntax = docker/dockerfile:1.4
########################################

FROM golang:1.20-buster AS develop

WORKDIR /src
COPY ["go.mod", "go.sum", "/src"]
RUN go mod download

########################################

FROM --platform=${BUILDPLATFORM} golang:1.20.3-alpine3.17 AS builder
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

FROM --platform=${TARGETARCH} gcr.io/distroless/static-debian11:nonroot AS controller
LABEL org.opencontainers.image.source = "https://github.com/sergelogvinov/proxmox-csi-plugin"

ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-controller-${TARGETARCH} /proxmox-csi-controller

ENTRYPOINT ["/proxmox-csi-controller"]

########################################

FROM --platform=${TARGETARCH} registry.k8s.io/build-image/debian-base:bullseye-v1.4.3 AS node
LABEL org.opencontainers.image.source = "https://github.com/sergelogvinov/proxmox-csi-plugin"

RUN apt-get update && apt-get install -y --no-install-recommends \
    mount \
    e2fsprogs \
    xfsprogs \
    && rm -rf /var/lib/apt/lists/*

ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-node-${TARGETARCH} /proxmox-csi-node

ENTRYPOINT ["/proxmox-csi-node"]
