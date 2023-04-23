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
RUN make build-all-archs

########################################

FROM --platform=${TARGETARCH} gcr.io/distroless/static-debian11:nonroot AS controller
LABEL org.opencontainers.image.source https://github.com/sergelogvinov/proxmox-csi-plugin

ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-${TARGETARCH} /proxmox-csi-controller

ENTRYPOINT ["/proxmox-csi-controller"]

########################################

FROM --platform=${TARGETARCH} gcr.io/distroless/static-debian11:nonroot AS node
LABEL org.opencontainers.image.source https://github.com/sergelogvinov/proxmox-csi-plugin

ARG TARGETARCH
COPY --from=builder /src/bin/proxmox-csi-node-${TARGETARCH} /proxmox-csi-node

ENTRYPOINT ["/proxmox-csi-node"]
