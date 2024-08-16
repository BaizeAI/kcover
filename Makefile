
CONTAINER_CLI ?= docker

HUB ?= release-ci.daocloud.io/baize

VERSION ?= dev-$(shell git rev-parse --short=8 HEAD)

image-%:
	 $(CONTAINER_CLI) buildx build \
 		-t $(HUB)/kcover-$*:$(VERSION) \
 		-f docker/$*.Dockerfile \
 		--push \
 		--platform linux/amd64,linux/arm64 \
 		.

images: image-controller image-agent

test: 
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: images
