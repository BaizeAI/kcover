
CONTAINER_CLI ?= docker

HUB ?= release-ci.daocloud.io/baize

VERSION ?= dev-$(shell git rev-parse --short=8 HEAD)

image-%:
	 $(CONTAINER_CLI) buildx build \
 		-t $(HUB)/fast-recovery-$*:$(VERSION) \
 		-f docker/$*.Dockerfile \
 		--push \
 		--platform linux/amd64,linux/arm64 \
 		.

images: image-controller image-agent

.PHONY: images
