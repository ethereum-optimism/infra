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
	gotestsum --format=pkgname-and-test-fails ./...

# Test with coverage (matches CI)
test-coverage:
	gotestsum --format=dots -- -coverpkg=github.com/ethereum-optimism/infra/... -coverprofile=coverage.out ./...

# Lint with same flags as CI
lint:
	golangci-lint run -E sqlclosecheck,bodyclose,asciicheck,misspell,errorlint --timeout 5m0s ./...

prep-acceptance:
	@echo "Tidying dependencies..."
	(cd testdata/acceptance && go mod tidy)

	@echo "Building tests..."
	(cd testdata/acceptance && go test -c ./...)

# Run real acceptance tests to test integration with op-devstack
# Note: pass a OP_ACCEPTOR_ARTIFACTS_URL environment variable to use custom artifacts
test-acceptance: build prep-acceptance
	@echo "Running op-acceptor..."
	./bin/op-acceptor --testdir ./testdata/acceptance --orchestrator sysgo --log.level INFO

test-all: build lint generate tidy test test-acceptance

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
	test-acceptance \
	test-all \
	lint \
	generate \
	tidy \
	ci-check \
	docker \
	start-monitoring \
	stop-monitoring
