.PHONY: build clean test run-daemon run-cli

GO := $(shell which go 2>/dev/null || echo /home/yumekaz/.local/go/bin/go)

build:
	mkdir -p bin
	GOSUMDB=off $(GO) build -o bin/cairn ./cmd/cairn
	GOSUMDB=off $(GO) build -o bin/cairnd ./cmd/cairnd

clean:
	rm -rf bin/

test:
	GOSUMDB=off $(GO) test -v ./...

run-daemon:
	GOSUMDB=off $(GO) run ./cmd/cairnd/main.go

run-cli:
	GOSUMDB=off $(GO) run ./cmd/cairn/main.go
