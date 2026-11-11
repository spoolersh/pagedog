all: generate_gtrace

_bin:
	mkdir -p _bin

gtrace_sources := $(shell find . -name '*_trace.go' -not -name '*_gtrace.go' -not -path '*/vendor/*')
gtrace_targets := $(patsubst %_trace.go,%_trace_gtrace.go,$(gtrace_sources))

# gtrace is a generator only: nothing in the module imports its runtime, so
# it installs by version, independent of go.mod.
gtrace_version := v0.7.0
_bin/gtrace: | _bin
	GOBIN=$(PWD)/_bin go install github.com/gobwas/gtrace/cmd/gtrace@$(gtrace_version)

$(gtrace_targets): %_trace_gtrace.go: %_trace.go _bin/gtrace
	$(eval changed = $(filter %.go,$?))
	$(if $(changed), PATH=$(PWD)/_bin:$(PATH) go generate -run gtrace $(changed))

.PHONY: generate_gtrace
generate_gtrace: $(gtrace_targets)

.PHONY: build
build: generate_gtrace
	go build ./...

.PHONY: test
test: generate_gtrace
	go test ./...

# The official image, spoolersh/pagedog, built inside docker from this
# checkout: a plain `docker build .` makes the same thing. The demo pulls
# it; `make pagedog-image` puts a local build under the tag instead.
pagedog_image := spoolersh/pagedog
# The published release: the image tag and the version label carry one
# string.
release_version := 0.1.0
# porcelain: staged and untracked count as dirty too.
stamp_dirty := $(shell [ -z "`git status --porcelain 2>/dev/null`" ] || echo -dirty)
stamp_revision := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo dev)$(stamp_dirty)
stamp_built := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Attestations are off: an attestation manifest adds an unknown/unknown
# entry to the tag on Docker Hub.

# Local single-arch build for smoke testing (buildx cannot --load multi-arch).
.PHONY: pagedog-image
pagedog-image: generate_gtrace
	docker buildx build --load --provenance=false --sbom=false \
		--build-arg VERSION=$(release_version) \
		--build-arg REVISION=$(stamp_revision) \
		--build-arg CREATED=$(stamp_built) \
		-t $(pagedog_image) .

# A version tag is pushed once: refuse a push whose tag already exists on
# the registry (:latest moves with every push, by design). Docker Hub
# keeps no history for an overwritten tag, so the guard is here.
.PHONY: pagedog-image-tag-free
pagedog-image-tag-free:
	@if docker buildx imagetools inspect $(pagedog_image):$(release_version) >/dev/null 2>&1; then \
		echo "$(pagedog_image):$(release_version) already exists on the registry; bump release_version" >&2; \
		exit 1; \
	fi

# Multi-arch build and push to the registry.
.PHONY: pagedog-image-push
pagedog-image-push: pagedog-image-tag-free generate_gtrace
	docker buildx build --push --platform linux/amd64,linux/arm64 --provenance=false --sbom=false \
		--build-arg VERSION=$(release_version) \
		--build-arg REVISION=$(stamp_revision) \
		--build-arg CREATED=$(stamp_built) \
		-t $(pagedog_image):$(release_version) \
		-t $(pagedog_image):latest .
