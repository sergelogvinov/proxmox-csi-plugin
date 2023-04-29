REGISTRY ?= ghcr.io
USERNAME ?= sergelogvinov
PROJECT ?= proxmox-csi
IMAGE ?= $(REGISTRY)/$(USERNAME)/$(PROJECT)
PLATFORM ?= linux/arm64,linux/amd64
PUSH ?= false

SHA ?= $(shell git describe --match=none --always --abbrev=8 --dirty)
TAG ?= $(shell git describe --tag --always --match v[0-9]\*)
GO_LDFLAGS := -ldflags "-w -s -X main.version=$(SHA)"

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

build-%:
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) \
		-o bin/proxmox-csi-$*-$(ARCH) ./cmd/$*

.PHONY: build
build: build-controller build-node ## Build

.PHONY: run
run: build-controller ## Run
	./bin/proxmox-csi-controller-$(ARCH) --cloud-config=hack/cloud-config.yaml -v=4

.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: unit
unit: ## Unit Tests
	go test -tags=unit $(shell go list ./...) $(TESTARGS)

############

.PHONY: helm-unit
helm-unit: ## Helm Unit Tests
	@helm lint charts/proxmox-csi-plugin
	@helm template -f charts/proxmox-csi-plugin/ci/values.yaml proxmox-csi-plugin charts/proxmox-csi-plugin >/dev/null

.PHONY: docs
docs:
	helm template -n kube-system proxmox-csi-plugin \
		-f charts/proxmox-csi-plugin/values.edge.yaml \
		-n csi-proxmox \
		charts/proxmox-csi-plugin > docs/deploy/proxmox-csi-plugin.yml
	helm template -n kube-system proxmox-csi-plugin \
		-f charts/proxmox-csi-plugin/values.talos.yaml \
		--set-string image.tag=$(TAG) \
		-n csi-proxmox \
		charts/proxmox-csi-plugin > docs/deploy/proxmox-csi-plugin-talos.yml

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

image-%:
	docker buildx build $(BUILD_ARGS) \
		--build-arg TAG=$(TAG) \
		--build-arg SHA=$(SHA) \
		-t $(IMAGE)-$*:$(TAG) \
		--target $* \
		-f Dockerfile .

.PHONY: images
images: image-controller image-node ## Build images
