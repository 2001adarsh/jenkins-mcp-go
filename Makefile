# jenkins-mcp-go — common development tasks.

BINARY      := jenkins-mcp
PKG         := github.com/2001adarsh/jenkins-mcp-go
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
BUILD_DIR   := bin

.PHONY: all build install test test-race vet lint fmt tidy clean run help

all: build

## build: Compile the binary to ./bin/$(BINARY).
build:
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY) .

## install: go install the binary to $GOBIN.
install:
	go install -trimpath -ldflags '$(LDFLAGS)' .

## test: Run unit tests.
test:
	go test ./...

## test-race: Run tests with the race detector.
test-race:
	go test -race ./...

## vet: Run go vet.
vet:
	go vet ./...

## lint: Run golangci-lint (requires golangci-lint installed).
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed. See https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run ./...

## fmt: Format Go sources and tidy modules.
fmt:
	gofmt -s -w .
	go mod tidy

## tidy: Tidy go.mod / go.sum.
tidy:
	go mod tidy

## clean: Remove build artifacts.
clean:
	rm -rf $(BUILD_DIR) dist coverage.out coverage.html

## run: Build and run the server (requires JENKINS_URL/JENKINS_USER/JENKINS_API_TOKEN in env).
run: build
	./$(BUILD_DIR)/$(BINARY)

## help: Show available targets.
help:
	@awk 'BEGIN{FS=":.*##"; printf "Targets:\n"} /^## [a-zA-Z_-]+:/ { sub("## ",""); printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
