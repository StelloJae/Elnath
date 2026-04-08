.PHONY: build test lint run clean version release release-snapshot install

BINARY := elnath
VERSION := 0.4.0
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/elnath

run: build
	./$(BINARY) run

test:
	go test -race -count=1 ./...

lint:
	go vet ./...
	@which staticcheck > /dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

clean:
	rm -f $(BINARY)
	rm -rf dist/

version: build
	./$(BINARY) version

install:
	go install $(LDFLAGS) ./cmd/elnath

release:
	goreleaser release --clean

release-snapshot:
	goreleaser release --snapshot --clean
