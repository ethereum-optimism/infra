GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')
VERSION := v0.0.1

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGSSTRING +=-X main.Version=$(VERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

all: build

build:
	go build -v $(LDFLAGS) -o ./bin/op-acceptor ./cmd

clean:
	rm -f ./bin/op-acceptor

test:
	gotestsum --format=standard-verbose ./...

# Test with coverage (matches CI)
test-coverage:
	gotestsum --format=standard-verbose -- -coverpkg=github.com/ethereum-optimism/infra/... -coverprofile=coverage.out ./...

# Lint with same flags as CI
lint:
	golangci-lint run -E sqlclosecheck,bodyclose,asciicheck,misspell,errorlint --timeout 5m0s ./...

# Generate code (if needed)
generate:
	go generate ./...

# Tidy dependencies (matches CI pre-check)
tidy:
	go mod tidy

# Run all CI checks locally
ci-check: tidy lint test-coverage

# Docker operations
docker:
	docker build ../ -f Dockerfile -t op-acceptor:latest

# Start prometheus and grafana for monitoring
start-monitoring:
	docker compose -f docker-compose.yml up -d --build

# Stop prometheus and grafana
stop-monitoring:
	docker compose -f docker-compose.yml down

.PHONY: \
	all \
	build \
	clean \
	test \
	test-coverage \
	lint \
	generate \
	tidy \
	ci-check \
	docker \
	start-monitoring \
	stop-monitoring
