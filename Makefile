GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test lint tidy vet

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

lint:
	$(GOLANGCI_LINT) run ./...
