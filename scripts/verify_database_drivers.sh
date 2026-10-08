#!/usr/bin/env bash
# Real database/client binaries; no published ports or production database data.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:?supply a new evidence directory}"
[[ ! -e "$OUT" ]] || { echo 'Refusing to overwrite database evidence' >&2; exit 2; }
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
PG_IMAGE="${CAIRN_TEST_POSTGRES_IMAGE:-postgres:latest}"
REDIS_IMAGE="${CAIRN_TEST_REDIS_IMAGE:-redis:7-bookworm}"
MONGO_IMAGE="${CAIRN_TEST_MONGO_IMAGE:-mongo:7}"
docker image inspect "$PG_IMAGE" "$REDIS_IMAGE" "$MONGO_IMAGE" >"$OUT/images.json"
git -C "$ROOT" rev-parse HEAD >"$OUT/cairn-commit.txt"
git -C "$ROOT/../DURAFLOW" rev-parse HEAD >"$OUT/duraflow-commit.txt"
git -C "$ROOT" diff --binary >"$OUT/cairn.patch"
git -C "$ROOT/../DURAFLOW" diff --binary >"$OUT/duraflow.patch"
cd "$ROOT"
env CAIRN_TEST_POSTGRES_IMAGE="$PG_IMAGE" CAIRN_TEST_REDIS_IMAGE="$REDIS_IMAGE" CAIRN_TEST_MONGO_IMAGE="$MONGO_IMAGE" \
  go test ./internal/daemon -run 'TestLive.*BackupRestore' -v -count=1 -timeout 5m >"$OUT/cairn-drivers.log" 2>&1
cd "$ROOT/../DURAFLOW"
CGO_ENABLED=0 go test -c ./pkg/store -o "$OUT/duraflow-store-tests"
DB_NAME="cairn-df-proof-$(id -u)-$(date -u +%Y%m%dT%H%M%S)-$RANDOM"
DB_ID="$(docker run -d --name "$DB_NAME" --network none --memory 512m --cpus 1 --pids-limit 128 \
  --tmpfs /var/lib/postgresql:rw,size=134217728 --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=acceptance "$PG_IMAGE")"
# Only remove the exact container created by this script; snapshots/logs stay.
cleanup() { docker logs "$DB_ID" >"$OUT/postgres.log" 2>&1 || true; docker rm -f "$DB_ID" >"$OUT/cleanup.log" 2>&1 || true; }
trap cleanup EXIT
ready=0
for attempt in $(seq 1 30); do
  if docker exec "$DB_ID" pg_isready -U postgres >"$OUT/readiness.log" 2>&1; then ready=1; break; fi
  sleep 1
done
[[ "$ready" == 1 ]] || { echo 'Disposable PostgreSQL did not become ready' >&2; exit 1; }
docker cp "$OUT/duraflow-store-tests" "$DB_ID:/duraflow-store-tests"
docker exec --env 'DURAFLOW_TEST_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/acceptance?sslmode=disable' \
  "$DB_ID" /duraflow-store-tests -test.run Postgres -test.v -test.timeout 2m >"$OUT/duraflow-postgres.log" 2>&1
printf 'PASS: real Cairn database drivers and DuraFlow PostgreSQL leases\n' >"$OUT/result.txt"
