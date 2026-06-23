.PHONY: build test lint install clean

build:
	go build -o bin/laccd-class-search-pp-cli ./cmd/laccd-class-search-pp-cli

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/laccd-class-search-pp-cli

clean:
	rm -rf bin/

build-mcp:
	go build -o bin/laccd-class-search-pp-mcp ./cmd/laccd-class-search-pp-mcp

install-mcp:
	go install ./cmd/laccd-class-search-pp-mcp

build-all: build build-mcp
