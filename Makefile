PKG     := github.com/mnorrsken/gpukoll
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)
IMAGE   ?= ghcr.io/mnorrsken/gpukoll
CHART   := charts/gpukoll

.PHONY: build run test race vet fmt clean docker-build helm-lint

build:
	go build -ldflags '$(LDFLAGS)' -o bin/gpukoll ./cmd/gpukoll

# Needs `kubectl proxy` running on port 8001.
run: build
	./bin/gpukoll -api http://127.0.0.1:8001

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf bin dist .helm-packages

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(VERSION) .

helm-lint:
	helm lint $(CHART)
	helm template gpukoll $(CHART) --api-versions route.openshift.io/v1/Route >/dev/null
