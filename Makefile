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
