.PHONY: run test lint build

run:
	go run cmd/volengine/main.go snapshot-run

build:
	go build -o bin/volengine cmd/volengine/main.go

test:
	go test ./...
