LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGSSTRING +=-X main.GitVersion=$(GITVERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

IMAGE_NAME ?= proxyd
IMAGE_TAG ?= latest
PLATFORM ?= linux/amd64

CHAINGUARD_BUILDER_IMAGE ?= chainguard/wolfi-base:latest
CHAINGUARD_RUNTIME_IMAGE ?= chainguard/wolfi-base:latest
DEV_BUILDER_IMAGE ?= chainguard/wolfi-base:latest
DEV_RUNTIME_IMAGE ?= chainguard/wolfi-base:latest

proxyd:
	go build -v $(LDFLAGS) -o ./bin/proxyd ./cmd/proxyd
.PHONY: proxyd

fmt:
	go mod tidy
	gofmt -w .
.PHONY: fmt

test:
	go test -v ./...
.PHONY: test

test-race:
	go test -race ./...
.PHONY: test-race

lint:
	golangci-lint run ./...
.PHONY: lint

test-%:
	go test -v ./... -test.run ^Test$*$$
.PHONY: test-%

docker:
	docker build \
		--platform $(PLATFORM) \
		--build-arg BUILDER_IMAGE=$(CHAINGUARD_BUILDER_IMAGE) \
		--build-arg RUNTIME_IMAGE=$(CHAINGUARD_RUNTIME_IMAGE) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-f Dockerfile ..
.PHONY: docker

docker-dev:
	docker build \
		--platform $(PLATFORM) \
		--build-arg BUILDER_IMAGE=$(DEV_BUILDER_IMAGE) \
		--build-arg RUNTIME_IMAGE=$(DEV_RUNTIME_IMAGE) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-f Dockerfile ..
.PHONY: docker-dev
