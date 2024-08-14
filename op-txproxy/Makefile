GIT_COMMIT := $(shell git rev-parse HEAD)
GIT_DATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GIT_COMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDGIT_DATEATE)
LDFLAGSSTRING +=-X main.Version=$(OP_CONDUCTOR_MON_VERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"


all: build

build:
	env GO111MODULE=on go build -v $(LDFLAGS) -o ./bin/op-txproxy ./cmd

clean:
	rm ./bin/op-txproxy

test:
	go test -v ./...

lint:
	golangci-lint run ./...

.PHONY: \
	build \
	clean \
	test \
	lint
