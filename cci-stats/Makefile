LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGSSTRING +=-X main.GitVersion=$(GITVERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

all: build

docker:
	docker build ../ -f Dockerfile -t cci-stats:latest

build:
	env GO111MODULE=on go build -v $(LDFLAGS) -o ./bin/cci-stats ./cmd/runner

clean:
	rm ./bin/cci-stats

test:
	go test -v ./...

lint:
	golangci-lint run ./...

.PHONY: \
	build \
	clean \
	test \
	lint \
	docker
