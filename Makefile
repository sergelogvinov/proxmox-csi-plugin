REGISTRY ?= ghcr.io
USERNAME ?= sergelogvinov
PROJECT ?= proxmox-csi-plugin
IMAGE ?= $(REGISTRY)/$(USERNAME)/$(PROJECT)
PLATFORM ?= linux/arm64,linux/amd64
PUSH ?= false

SHA ?= $(shell git describe --match=none --always --abbrev=8 --dirty)
TAG ?= $(shell git describe --tag --always --match v[0-9]\*)

OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)
ARCHS = amd64 arm64

BUILD_ARGS := --platform=$(PLATFORM)
ifeq ($(PUSH),true)
BUILD_ARGS += --push=$(PUSH)
else
BUILD_ARGS += --output type=docker
endif

############

# Help Menu

define HELP_MENU_HEADER
# Getting Started

To build this project, you must have the following installed:

- git
- make
- golang 1.20+
- golangci-lint

endef

export HELP_MENU_HEADER

help: ## This help menu
	@echo "$$HELP_MENU_HEADER"
	@grep -E '^[a-zA-Z0-9%_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

############
#
# Build Abstractions
#

build-all-archs:
	@for arch in $(ARCHS); do $(MAKE) ARCH=$${arch} build ; done

.PHONY: clean
clean: ## Clean
	rm -rf bin .cache

.PHONY: build
build: ## Build
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) \
		-o bin/proxmox-csi-$(ARCH) ./cmd/controller
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) \
		-o bin/proxmox-csi-node-$(ARCH) ./cmd/node

.PHONY: run
run: build ## Run
	# ./bin/proxmox-csi-node-$(ARCH) --cloud-config=hack/cloud-config.yaml -v=5
	./bin/proxmox-csi-$(ARCH) --cloud-config=hack/cloud-config.yaml -v=4

.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: unit
unit: ## Unit Tests
	go test -tags=unit $(shell go list ./...) $(TESTARGS)

.PHONY: docs
docs:
	helm template -n kube-system proxmox-csi-plugin \
		-f charts/proxmox-csi-plugin/values.dev.yaml \
		--set-string image.tag=$(TAG) \
		--include-crds \
		-n csi-proxmox \
		charts/proxmox-csi-plugin > docs/deploy/proxmox-csi-plugin.yml

############
#
# Docker Abstractions
#

.PHONY: docker-init
docker-init:
	docker run --rm --privileged multiarch/qemu-user-static:register --reset

	docker context create multiarch ||:
	docker buildx create --name multiarch --driver docker-container --use ||:
	docker context use multiarch
	docker buildx inspect --bootstrap multiarch

.PHONY: images
images:
	# @docker buildx build $(BUILD_ARGS) \
	# 	--build-arg TAG=$(TAG) \
	# 	-t $(IMAGE):$(TAG) \
	# 	--target controller \
	# 	-f Dockerfile .
	@docker buildx build $(BUILD_ARGS) \
		--build-arg TAG=$(TAG) \
		-t $(IMAGE)-node:$(TAG) \
		--target node \
		-f Dockerfile .
