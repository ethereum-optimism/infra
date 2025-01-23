GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')
VERSION := v0.0.1

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGSSTRING +=-X main.Version=$(VERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

all: build

build:
	go build -v $(LDFLAGS) -o ./bin/op-nat ./cmd

clean:
	rm ./bin/op-nat

test:
	go test -v ./...

lint:
	golangci-lint run ./...

.PHONY: \
	build \
	clean \
	test \
	lint
