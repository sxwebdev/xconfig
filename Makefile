MODULES := . decoders/xconfigdotenv decoders/xconfigjson decoders/xconfigyaml \
	sourcers/xconfigvault watchers/xconfigfsnotify tests/integration

.PHONY: fmt workspace lint test all release-check release-snapshot

fmt:
	gofumpt -l -w .

workspace:
	go run tools/dev.go workspace

lint: workspace
	for m in $(MODULES); do (cd $$m && golangci-lint run ./...) || exit 1; done

test:
	go run tools/dev.go check

all: lint test

release-check:
	go run tools/release.go
	go test -race -count=1 tools/release.go tools/release_test.go
	go test -race -count=1 tools/dev.go tools/dev_test.go
	goreleaser check

release-snapshot:
	goreleaser release --snapshot --clean
