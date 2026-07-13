
CONTAINER_CLI ?= docker

HUB ?= release-ci.daocloud.io/baize

VERSION ?= dev-$(shell git rev-parse --short=8 HEAD)

BUILD_PLATFORM ?= linux/amd64
CONTROLLER_PUSH_PLATFORMS ?= linux/amd64,linux/arm64
AGENT_PUSH_PLATFORMS ?= linux/amd64,linux/arm64
AGENT_METAX_PUSH_PLATFORMS ?= linux/amd64
MX_SMI_IMAGE ?= ghcr.io/baizeai/mx-smi:v0.2

build-controller:
	$(CONTAINER_CLI) build \
		-t $(HUB)/kcover-controller:$(VERSION) \
		-f docker/kcover.Dockerfile \
		--platform $(BUILD_PLATFORM) \
		.

push-controller:
	$(CONTAINER_CLI) buildx build \
		-t $(HUB)/kcover-controller:$(VERSION) \
		-f docker/kcover.Dockerfile \
		--push \
		--provenance=false \
		--platform $(CONTROLLER_PUSH_PLATFORMS) \
		.

build-mx-smi:
	$(CONTAINER_CLI) build \
		-t $(MX_SMI_IMAGE) \
		-f docker/mx-smi.Dockerfile \
		--platform linux/amd64 \
		.

push-mx-smi: build-mx-smi
	$(CONTAINER_CLI) push $(MX_SMI_IMAGE)

image-mx-smi: push-mx-smi

build-agent:
	$(CONTAINER_CLI) build \
		-t $(HUB)/kcover-agent:$(VERSION) \
		-f docker/agent.Dockerfile \
		--platform $(BUILD_PLATFORM) \
		.

push-agent:
	$(CONTAINER_CLI) buildx build \
		-t $(HUB)/kcover-agent:$(VERSION) \
		-f docker/agent.Dockerfile \
		--push \
		--provenance=false \
		--platform $(AGENT_PUSH_PLATFORMS) \
		.

build-agent-metax:
	$(CONTAINER_CLI) build \
		-t $(HUB)/kcover-agent-metax:$(VERSION) \
		-f docker/agent-metax.Dockerfile \
		--build-arg MX_SMI_IMAGE=$(MX_SMI_IMAGE) \
		--platform $(BUILD_PLATFORM) \
		.

push-agent-metax:
	$(CONTAINER_CLI) buildx build \
		-t $(HUB)/kcover-agent-metax:$(VERSION) \
		-f docker/agent-metax.Dockerfile \
		--build-arg MX_SMI_IMAGE=$(MX_SMI_IMAGE) \
		--push \
		--provenance=false \
		--platform $(AGENT_METAX_PUSH_PLATFORMS) \
		.

build: build-controller build-agent

build-all: build-controller build-agent build-agent-metax

push: push-controller push-agent push-agent-metax

image-agent: push-agent

image-agent-metax: push-agent-metax

image-controller: push-controller

images: push-controller push-agent push-agent-metax

test:
	go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

test-metax:
	go test -tags=metax $$(go list ./... | grep -v /e2e)

test-all: test test-metax

helm-test:
	./hack/verify-helm.sh

.PHONY: build build-all build-agent build-agent-metax build-controller build-mx-smi push push-agent push-agent-metax push-controller push-mx-smi images image-mx-smi image-agent image-agent-metax image-controller test test-metax test-all helm-test
