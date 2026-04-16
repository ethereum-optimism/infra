LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGSSTRING +=-X main.GitVersion=$(GITVERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

IMAGE_NAME ?= proxyd
IMAGE_TAG ?= latest
PLATFORM ?= linux/amd64

DHI_BUILDER_IMAGE ?= dhi.io/golang:1.26-alpine3.23-dev
DHI_RUNTIME_IMAGE ?= dhi.io/alpine-base:3.23
DEV_BUILDER_IMAGE ?= golang:1.26.1-alpine3.23
DEV_RUNTIME_IMAGE ?= alpine:3.23

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

lint:
	go vet ./...
	goimports -w .
.PHONY: test

test-%:
	go test -v ./... -test.run ^Test$*$$
.PHONY: test-%

docker:
	docker build \
		--platform $(PLATFORM) \
		--build-arg BUILDER_IMAGE=$(DHI_BUILDER_IMAGE) \
		--build-arg RUNTIME_IMAGE=$(DHI_RUNTIME_IMAGE) \
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
