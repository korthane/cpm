.PHONY: build test lint run

build:
	go build -o cpm ./cmd/cpm

test:
	go test ./...

lint:
	golangci-lint run

run:
	go run ./cmd/cpm
