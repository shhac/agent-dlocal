BINARY := agent-dlocal
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOCACHE ?= $(CURDIR)/.cache/go-build

build:
	GOCACHE=$(GOCACHE) go build -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/agent-dlocal

build-mock:
	GOCACHE=$(GOCACHE) go build -buildvcs=false -o mockdlocal ./cmd/mockdlocal

mock:
	GOCACHE=$(GOCACHE) go run ./cmd/mockdlocal

mock-dev:
	AGENT_DLOCAL_BASE_URL=http://127.0.0.1:12112 \
	DLOCAL_X_LOGIN=mocklogin DLOCAL_X_TRANS_KEY=mocktrans DLOCAL_SECRET_KEY=mocksecret \
	GOCACHE=$(GOCACHE) go run ./cmd/agent-dlocal $(ARGS)

test:
	GOCACHE=$(GOCACHE) go test ./... -count=1

test-short:
	GOCACHE=$(GOCACHE) go test ./... -count=1 -short

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	@command -v goimports >/dev/null && goimports -w . || echo "goimports not installed (optional; install: go install golang.org/x/tools/cmd/goimports@latest)"

clean:
	rm -f $(BINARY)
	rm -f mockdlocal
	rm -rf dist/

dev:
	GOCACHE=$(GOCACHE) go run ./cmd/agent-dlocal $(ARGS)

vet:
	GOCACHE=$(GOCACHE) go vet ./...

.PHONY: build build-mock mock mock-dev test test-short lint fmt clean dev vet
