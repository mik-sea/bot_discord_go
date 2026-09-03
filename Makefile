.PHONY: run build tidy lint test clean release

BIN := bin/bot

## run: run the bot (reads from .env if dotenv is sourced)
run:
	go run ./cmd/bot/...

## build: compile binary to bin/bot
build:
	go build -o $(BIN) ./cmd/bot/...

## tidy: tidy and verify go modules
tidy:
	go mod tidy
	go mod verify

## lint: run staticcheck linter
lint:
	staticcheck ./...

## test: run all tests with race detector
test:
	go test -race -v ./...

## clean: remove compiled binary
clean:
	rm -rf bin/

## release: build docker image, tag with git sha (+ latest), push to GHCR
release:
	./scripts/release.sh
