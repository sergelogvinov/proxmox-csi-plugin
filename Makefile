REGISTRY ?= ghcr.io
USERNAME ?= sergelogvinov
OCIREPO ?= $(REGISTRY)/$(USERNAME)
HELMREPO ?= $(REGISTRY)/$(USERNAME)/charts
PLATFORM ?= linux/arm64,linux/amd64
PUSH ?= false

SHA ?= $(shell git describe --match=none --always --abbrev=7 --dirty)
TAG ?= $(shell git describe --tag --always --match v[0-9]\*)
GO_LDFLAGS := -ldflags "-w -s -X main.version=$(TAG) -X main.commit=$(SHA)"

OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)
ARCHS = amd64 arm64

BUILD_ARGS := --platform=$(PLATFORM)
ifeq ($(PUSH),true)
BUILD_ARGS += --push=$(PUSH)
BUILD_ARGS += --output type=image,annotation-index.org.opencontainers.image.source="https://github.com/$(USERNAME)/proxmox-csi-plugin",annotation-index.org.opencontainers.image.description="Proxmox VE CSI plugin"
else
BUILD_ARGS += --output type=docker
endif

COSING_ARGS ?=

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

build-pvecsictl:
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) \
		-o bin/pvecsictl-$(ARCH) ./cmd/pvecsictl

build-%:
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) \
		-o bin/proxmox-csi-$*-$(ARCH) ./cmd/$*

.PHONY: build
build: build-controller build-node build-pvecsictl ## Build

.PHONY: run
run: build-controller ## Run
	go run $(GO_LDFLAGS) -race ./cmd/controller/main.go --cloud-config=hack/cloud-config.yaml -v=5 --metrics-address=:8080
	# ./bin/proxmox-csi-controller-$(ARCH) --cloud-config=hack/cloud-config.yaml -v=5 --metrics-address=:8080

.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: unit
unit: ## Unit Tests
	go test -tags=unit $(shell go list ./...) $(TESTARGS)

.PHONY: test
test: lint unit ## Run all tests

.PHONY: conformance
conformance: ## Conformance
	docker run --rm -it -v $(PWD):/src -w /src ghcr.io/siderolabs/conform:v0.1.0-alpha.30 enforce

############

.PHONY: helm-unit
helm-unit: ## Helm Unit Tests
	@helm lint charts/proxmox-csi-plugin
	@helm template -f charts/proxmox-csi-plugin/ci/values.yaml proxmox-csi-plugin charts/proxmox-csi-plugin >/dev/null

.PHONY: helm-login
helm-login: ## Helm Login
	@echo "${HELM_TOKEN}" | helm registry login $(REGISTRY) --username $(USERNAME) --password-stdin

.PHONY: helm-release
helm-release: ## Helm Release
	@rm -rf dist/
	@helm package charts/proxmox-csi-plugin -d dist
	@helm push dist/proxmox-csi-plugin-*.tgz oci://$(HELMREPO) 2>&1 | tee dist/.digest
	@cosign sign --yes $(COSING_ARGS) $(HELMREPO)/proxmox-csi-plugin@$$(cat dist/.digest | awk -F "[, ]+" '/Digest/{print $$NF}')

############

.PHONY: docs
docs:
	yq -i '.appVersion = "$(TAG)"' charts/proxmox-csi-plugin/Chart.yaml
	helm template -n csi-proxmox proxmox-csi-plugin \
		-f charts/proxmox-csi-plugin/values.edge.yaml \
		charts/proxmox-csi-plugin > docs/deploy/proxmox-csi-plugin.yml
	helm template -n csi-proxmox proxmox-csi-plugin \
		--set-string image.tag=$(TAG) \
		--set createNamespace=true \
		charts/proxmox-csi-plugin > docs/deploy/proxmox-csi-plugin-release.yml
	helm template -n csi-proxmox proxmox-csi-plugin \
		-f charts/proxmox-csi-plugin/values.talos.yaml \
		--set-string image.tag=$(TAG) \
		charts/proxmox-csi-plugin > docs/deploy/proxmox-csi-plugin-talos.yml
	helm-docs --sort-values-order=file charts/proxmox-csi-plugin

release-update:
	git-chglog --config hack/chglog-config.yml -o CHANGELOG.md

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
		-t $(OCIREPO)/$*:$(TAG) \
		--target $* \
		-f Dockerfile .

.PHONY: images-checks
images-checks: images image-tools-check
	trivy image --exit-code 1 --ignore-unfixed --severity HIGH,CRITICAL --no-progress $(OCIREPO)/proxmox-csi-controller:$(TAG)
	trivy image --exit-code 1 --ignore-unfixed --severity HIGH,CRITICAL --no-progress $(OCIREPO)/proxmox-csi-node:$(TAG)
	trivy image --exit-code 1 --ignore-unfixed --severity HIGH,CRITICAL --no-progress $(OCIREPO)/pvecsictl:$(TAG)

.PHONY: images-cosign
images-cosign:
	@cosign sign --yes $(COSING_ARGS) --recursive $(OCIREPO)/proxmox-csi-controller:$(TAG)
	@cosign sign --yes $(COSING_ARGS) --recursive $(OCIREPO)/proxmox-csi-node:$(TAG)
	@cosign sign --yes $(COSING_ARGS) --recursive $(OCIREPO)/pvecsictl:$(TAG)

.PHONY: images
images: image-proxmox-csi-controller image-proxmox-csi-node image-pvecsictl ## Build images
