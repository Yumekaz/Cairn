.PHONY: build clean test smoke demo gate matrix run-daemon run-cli

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
	@for s in scripts/*.sh; do bash -n "$$s"; done
	@echo "smoke OK (full gate: make gate — needs Mini-Docker)"

# Local reliability gate (units + optional live demos)
gate: build
	chmod +x scripts/*.sh
	N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh

# Failure matrix F1–F4,F6
matrix: build
	chmod +x scripts/*.sh
	CASE=F1,F2,F3,F4,F6 ./scripts/failure_matrix.sh

# Full cold-start demo (requires Mini-Docker rootfs + daemon)
demo: build
	chmod +x scripts/clean_demo.sh
	./scripts/clean_demo.sh

run-daemon:
	GOSUMDB=off $(GO) run ./cmd/cairnd/main.go

run-cli:
	GOSUMDB=off $(GO) run ./cmd/cairn/main.go
