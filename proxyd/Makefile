LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGSSTRING +=-X main.GitVersion=$(GITVERSION)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

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
