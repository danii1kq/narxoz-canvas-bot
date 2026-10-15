BINARY      := bin/bot
IMAGE       := narxoz-canvas-bot
.DEFAULT_GOAL := all

.PHONY: all build run test vet fmt tidy clean docker compose logs

all: build

## build: compile the binary into ./bin/bot (CGO disabled, pure-Go sqlite)
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/bot

## run: run the bot directly with go run (reads .env from the shell)
run:
	go run ./cmd/bot

## test: run all unit tests
test:
	go test ./... -count=1

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go files
fmt:
	gofmt -w .

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy

## docker: build the Docker image
docker:
	docker build -t $(IMAGE) .

## compose: build and start via docker compose (detached)
compose:
	docker compose up -d --build

## logs: follow container logs
logs:
	docker compose logs -f

## clean: remove build artifacts
clean:
	rm -rf bin
