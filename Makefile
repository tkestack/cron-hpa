.PHONY: all
all: verify-gofmt
	hack/build.sh

.PHONY: clean
clean:
	rm -rf bin/ _output/ go .version-defs

.PHONY: build
build:
	hack/build.sh

# ==============================================================================
# Includes

include build/lib/common.mk
include build/lib/image.mk

.PHONY: verify
verify:
	hack/verify-all.sh

.PHONY: verify-gofmt
verify-gofmt:
	hack/verify-gofmt.sh

format:
	hack/format.sh

image:
	hack/build-image.sh

## release.multiarch: Build docker images for multiple platforms and push manifest lists to registry.
.PHONY: release.multiarch
release.multiarch:
	@$(MAKE) image.manifest.push.multiarch BINS="cron-hpa-controller"

#  vim: set ts=2 sw=2 tw=0 noet :
