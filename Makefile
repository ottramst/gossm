# Releases are cut from the GitHub Actions UI (Release workflow) or by
# pushing a vX.Y.Z tag; CI builds and publishes them via GoReleaser.

BINARY   := gossm
COVERAGE := coverage.txt

.PHONY: all build test lint vet snapshot clean

all: build

build:
	go build -o $(BINARY) .

test:
	go test ./... --count 1 -race -covermode=atomic -coverprofile=$(COVERAGE)

lint:
	golangci-lint run ./...

vet:
	go vet ./...

## Build a local release snapshot without publishing (requires goreleaser)
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf dist $(BINARY) $(BINARY).exe $(COVERAGE)
