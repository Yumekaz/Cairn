.PHONY: build clean test smoke run-daemon run-cli demo

GO := $(shell which go 2>/dev/null || echo go)

build:
	mkdir -p bin
	GOSUMDB=off $(GO) build -o bin/cairn ./cmd/cairn
	GOSUMDB=off $(GO) build -o bin/cairnd ./cmd/cairnd

clean:
	rm -rf bin/

test:
	GOSUMDB=off $(GO) test -count=1 ./internal/deploymeta/ ./internal/config/ ./internal/preflight/ ./internal/store/ ./internal/daemon/

# CI-friendly smoke: unit tests + build + demo script syntax
smoke: test build
	bash -n scripts/clean_demo.sh
	@echo "smoke OK (live demo: make demo with Mini-Docker + sudo as needed)"

# Full cold-start demo (requires Mini-Docker rootfs + daemon)
demo: build
	chmod +x scripts/clean_demo.sh
	./scripts/clean_demo.sh

run-daemon:
	GOSUMDB=off $(GO) run ./cmd/cairnd/main.go

run-cli:
	GOSUMDB=off $(GO) run ./cmd/cairn/main.go
