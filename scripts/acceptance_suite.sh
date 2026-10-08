#!/usr/bin/env bash
# Repeatable, bounded test runner for the six-repository Cairn project suite.
# Results and per-check logs are kept under validation-runs/ (never /tmp).

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PARENT="$(cd "$ROOT/.." && pwd)"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="${ACCEPTANCE_OUTPUT_DIR:-$ROOT/validation-runs/$STAMP}"
TIME_LIMIT="${ACCEPTANCE_TIMEOUT:-20m}"

if [[ -e "$OUT" ]]; then
  printf 'Refusing to overwrite existing acceptance output: %s\n' "$OUT" >&2
  exit 2
fi
mkdir -p "$OUT/logs" "$OUT/tmp" "$OUT/state/minidb"
# Normalize inherited relative output paths before changing repository cwd.
OUT="$(cd "$OUT" && pwd)"
export TMPDIR="$(mktemp -d "${HOME}/ct.XXXXXXXX")"
export MINIDB_TEST_STATE_DIR="$OUT/state/minidb"
export PYTHONDONTWRITEBYTECODE=1
RESULTS="$OUT/results.tsv"
REPORT="$OUT/report.md"
CURRENT_CHECK=""
CURRENT_STARTED=""
ALL_CHECKS=(cairn-go cairn-live-integration duraflow-go minidocker-pytest failforge-go failforge-build failforge-minidb-42 failforge-minidb-43 failforge-coordination-baseline failforge-coordination-42 failforge-coordination-43 minidb-pytest coordination-pytest minidocker-root-runtime duraflow-postgres cairn-real-database-backups cairn-dashboard-browser host-reboot-and-crash disk-full-and-power-loss sustained-soak hostile-workload-security)

printf 'status\tcheck\texit_code\tstarted_utc\tended_utc\tnote\n' >"$RESULTS"

record() {
  local status="$1" name="$2" code="$3" started="$4" ended="$5" note="$6"
  note="${note//$'\t'/ }"
  note="${note//$'\n'/ }"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$status" "$name" "$code" "$started" "$ended" "$note" >>"$RESULTS"
  write_report
}

capture_revisions() {
  local name="$1" dir="$2" head dirty branch
  if [[ ! -d "$dir/.git" && ! -f "$dir/.git" ]]; then
    printf '%s\tMISSING\tUNKNOWN\tUNKNOWN\n' "$name" >>"$OUT/revisions.tsv"
    return
  fi
  head="$(git -C "$dir" rev-parse HEAD 2>/dev/null || printf 'UNKNOWN')"
  branch="$(git -C "$dir" branch --show-current 2>/dev/null || printf 'UNKNOWN')"
  dirty="$(git -C "$dir" status --short --untracked-files=normal 2>/dev/null | tr '\n' ';' | sed 's/;*$//')"
  printf '%s\t%s\t%s\t%s\n' "$name" "$head" "$branch" "${dirty:-clean}" >>"$OUT/revisions.tsv"
  git -C "$dir" diff --binary >"$OUT/$name.patch"
  git -C "$dir" ls-files --others --exclude-standard -z |
    tar -C "$dir" --null -T - -cf "$OUT/$name-untracked.tar"
}

write_report() {
  local state="${1:-running}"
  {
    printf '# Six-project acceptance run\n\n'
    printf -- '- Run: `%s`\n- State: **%s**\n- Host: `%s`\n- Per-check limit: `%s`\n- Results: `results.tsv`\n\n' "$STAMP" "$state" "$(uname -srmo 2>/dev/null || echo unknown)" "$TIME_LIMIT"
    printf 'Short scratch directory: `%s`. Persistent evidence remains in this report directory.\n\n' "$TMPDIR"
    printf '## Repository revisions\n\n| Project | Branch | Commit | Working tree |\n| --- | --- | --- | --- |\n'
    if [[ -f "$OUT/revisions.tsv" ]]; then
      while IFS=$'\t' read -r project rev branch dirty; do
        printf '| %s | `%s` | `%s` | %s |\n' "$project" "$branch" "$rev" "$dirty"
      done <"$OUT/revisions.tsv"
    fi
    printf '\n## Checks\n\n| Result | Check | Exit | Note | Log |\n| --- | --- | ---: | --- | --- |\n'
    while IFS=$'\t' read -r status check code started ended note; do
      [[ "$status" == status ]] && continue
      printf '| **%s** | %s | %s | %s | [`%s.log`](logs/%s.log) |\n' "$status" "$check" "$code" "$note" "$check" "$check"
    done <"$RESULTS"
    printf '\n## Open environment gates\n\n'
    printf '%s\n' \
      '- Browser/dashboard acceptance is interactive and is not inferred from unit tests; see `docs/VALIDATION_2026-09.md`.' \
      '- Real PostgreSQL, Redis, and MongoDB data-path tests need explicit disposable database endpoints; mocked command tests do not count as real-database proof.' \
      '- Root-only runtime tests, VM reboot, true disk-full/power-loss tests, multi-day soak, and independent hostile-workload review require a separately isolated environment.'
  } >"$REPORT"
}

on_interrupt() {
  local sig="$1" now
  now="$(date -u +%FT%TZ)"
  if [[ -n "$CURRENT_CHECK" ]]; then
    record INTERRUPTED "$CURRENT_CHECK" 130 "${CURRENT_STARTED:-unknown}" "$now" "terminated by $sig; inspect partial log"
    CURRENT_CHECK=""
  fi
  for check in "${ALL_CHECKS[@]}"; do
    if ! awk -F '\t' -v name="$check" '$2 == name { found=1 } END { exit !found }' "$RESULTS"; then
      record SKIPPED "$check" 130 "$now" "$now" "not started because this run was interrupted"
    fi
  done
  write_report interrupted
  printf '\nInterrupted. Partial results retained at %s\n' "$OUT" >&2
  exit 130
}
trap 'on_interrupt INT' INT
trap 'on_interrupt TERM' TERM

run_check() {
  local name="$1" dir="$2" note="$3" start end rc log
  shift 3
  CURRENT_CHECK="$name"
  start="$(date -u +%FT%TZ)"
  CURRENT_STARTED="$start"
  log="$OUT/logs/$name.log"
  printf '[RUN ] %-30s %s\n' "$name" "$note"
  (
    # Bound individual fixture/log files and stop adding work if this run has
    # exceeded its artifact budget or the destination filesystem is low.
    ulimit -f 262144
    if (( $(du -sm "$OUT" | awk '{print $1}') > ${ACCEPTANCE_MAX_ARTIFACT_MB:-512} )); then
      printf 'BLOCKED: acceptance artifact budget exceeded\n'; exit 125
    fi
    if (( $(df -Pm "$OUT" | awk 'NR==2 {print $4}') < 1024 )); then
      printf 'BLOCKED: less than 1 GiB free on the artifact filesystem\n'; exit 125
    fi
    printf 'Started: %s\nDirectory: %s\nCommand:' "$start" "$dir"
    printf ' %q' "$@"
    printf '\n\n'
    if [[ ! -d "$dir" ]]; then
      printf 'BLOCKED: repository directory is missing\n'
      exit 125
    fi
    if ! command -v timeout >/dev/null 2>&1; then
      printf 'BLOCKED: GNU timeout is required to bound this check\n'
      exit 125
    fi
    cd "$dir" || exit 125
    timeout --signal=TERM --kill-after=10s "$TIME_LIMIT" "$@"
  ) >"$log" 2>&1
  rc=$?
  end="$(date -u +%FT%TZ)"
  if [[ $rc -eq 0 ]]; then
    record PASS "$name" "$rc" "$start" "$end" "$note"
    printf '[PASS] %-30s (%s)\n' "$name" "$log"
  elif [[ $rc -eq 124 || $rc -eq 137 ]]; then
    record BLOCKED "$name" "$rc" "$start" "$end" "timed out after $TIME_LIMIT; see log"
    printf '[TIME] %-30s (see %s)\n' "$name" "$log"
  elif [[ $rc -eq 125 ]]; then
    record BLOCKED "$name" "$rc" "$start" "$end" "required repository/tool unavailable; see log"
    printf '[BLOCK] %-29s (see %s)\n' "$name" "$log"
  else
    record FAIL "$name" "$rc" "$start" "$end" "command failed; inspect log"
    printf '[FAIL] %-30s exit=%s (see %s)\n' "$name" "$rc" "$log"
  fi
  CURRENT_CHECK=""
  CURRENT_STARTED=""
}

record_blocked() {
  local name="$1" note="$2" now
  now="$(date -u +%FT%TZ)"
  record BLOCKED "$name" 125 "$now" "$now" "$note"
  printf '%s\n' "$note" >"$OUT/logs/$name.log"
}

: >"$OUT/revisions.tsv"
capture_revisions Cairn "$ROOT"
capture_revisions Mini-Docker "$PARENT/Mini-Docker"
capture_revisions DuraFlow "$PARENT/DURAFLOW"
capture_revisions FailForge "$PARENT/FAILFORGE"
capture_revisions MiniDB "$PARENT/Mini-Redis-Cassandra"
capture_revisions Coordination "$PARENT/Coordination-service"

ROOTFS="${CAIRN_ROOTFS:-$PARENT/Mini-Docker/rootfs}"
if [[ -x "$ROOTFS/bin/busybox" || -x "$ROOTFS/bin/sh" ]]; then
  run_check cairn-go "$ROOT" 'isolated Go unit packages' env "CAIRN_ROOTFS=$ROOTFS" go test ./cmd/... ./internal/... -count=1 -timeout 15m
  if [[ "${ACCEPTANCE_LIVE_CAIRN:-0}" == 1 ]]; then
    run_check cairn-live-integration "$ROOT" 'explicit live fixtures; requires disposable Cairn home/runtime' env "CAIRN_ROOTFS=$ROOTFS" go test ./tests/integration -count=1 -timeout 15m
  else
    record_blocked cairn-live-integration 'legacy fixtures use the default Cairn home and counter-api; opt in only on a disposable instance with ACCEPTANCE_LIVE_CAIRN=1'
  fi
else
  record_blocked cairn-go "Mini-Docker rootfs not found at $ROOTFS; set CAIRN_ROOTFS"
fi
run_check duraflow-go "$PARENT/DURAFLOW" 'go test ./... (Postgres DSN integration test may skip)' go test ./... -count=1 -timeout 15m
run_check minidocker-pytest "$PARENT/Mini-Docker" 'Python suite (root-gated cases report their own skips)' python3 -m pytest -q -p no:cacheprovider
run_check failforge-go "$PARENT/FAILFORGE" 'go test ./...' go test ./... -count=1 -timeout 15m
run_check failforge-build "$PARENT/FAILFORGE" 'build the tested FailForge CLI into this run artifact directory' go build -o "$OUT/failforge" ./cmd/failforge
run_check failforge-minidb-42 "$PARENT/FAILFORGE" 'MiniDB seed 42; checks configured successful-operation and fault-injection coverage' "$OUT/failforge" run failforge_minidb.yml --seed 42
run_check failforge-minidb-43 "$PARENT/FAILFORGE" 'MiniDB seed 43; second deterministic coverage campaign' "$OUT/failforge" run failforge_minidb.yml --seed 43
run_check failforge-coordination-baseline "$PARENT/FAILFORGE" 'Coordination no-fault write and lock baseline' "$OUT/failforge" run failforge_coordination_baseline.yml --seed 42
run_check failforge-coordination-42 "$PARENT/FAILFORGE" 'Coordination seed 42; checks leader/lock invariants plus workload and fault coverage' "$OUT/failforge" run failforge_coordination.yml --seed 42
run_check failforge-coordination-43 "$PARENT/FAILFORGE" 'Coordination seed 43; second deterministic coverage campaign' "$OUT/failforge" run failforge_coordination.yml --seed 43
run_check minidb-pytest "$PARENT/Mini-Redis-Cassandra" 'Python suite with dedicated run state' python3 -m pytest -q -p no:cacheprovider tests
run_check coordination-pytest "$PARENT/Coordination-service" 'cluster, handoff, recovery and concurrency tests' python3 -m pytest -q -p no:cacheprovider

if [[ "$(id -u)" -eq 0 ]]; then
  run_check minidocker-root-runtime "$PARENT/Mini-Docker" 'root-gated namespace/container tests' python3 -m pytest -q tests/test_linux_runtime_integration.py
else
  record_blocked minidocker-root-runtime 'not run as root; run this check in the approved privileged disposable environment'
fi

if [[ -n "${DURAFLOW_TEST_POSTGRES_DSN:-}" ]]; then
  run_check duraflow-postgres "$PARENT/DURAFLOW" 'configured PostgreSQL store integration' go test ./pkg/store -run Postgres -count=1 -timeout 5m
elif [[ "${ACCEPTANCE_DATABASES:-0}" != 1 ]]; then
  record_blocked duraflow-postgres 'DURAFLOW_TEST_POSTGRES_DSN is not configured; suite skip is not treated as proof'
fi

if [[ "${ACCEPTANCE_DATABASES:-0}" == 1 ]]; then
  run_check cairn-real-database-backups "$ROOT" 'real private PostgreSQL/Redis/MongoDB drivers plus DuraFlow PostgreSQL leases' bash scripts/verify_database_drivers.sh "$OUT/databases"
  if [[ -z "${DURAFLOW_TEST_POSTGRES_DSN:-}" ]]; then
    if [[ -f "$OUT/databases/result.txt" ]]; then
      cp "$OUT/databases/duraflow-postgres.log" "$OUT/logs/duraflow-postgres.log"
      now="$(date -u +%FT%TZ)"
      record PASS duraflow-postgres 0 "$now" "$now" 'executed in the private database gate; exact command/log retained in databases/'
    else
      record_blocked duraflow-postgres 'private database proof did not complete; inspect the database gate log'
    fi
  fi
else
  record_blocked cairn-real-database-backups 'set ACCEPTANCE_DATABASES=1 for cached official database images and Docker; mocked command-path tests do not count'
fi
record_blocked cairn-dashboard-browser 'manual browser acceptance is not inferred from this shell runner; consult the retained validation record'
record_blocked host-reboot-and-crash 'requires an isolated disposable VM; the host is not rebooted by this script'
record_blocked disk-full-and-power-loss 'requires a bounded disposable filesystem/VM; this script will not fill host storage'
record_blocked sustained-soak '24-72 hour monitored run is not completed by this bounded test suite'
record_blocked hostile-workload-security 'requires a separate threat model and isolated adversarial review'

write_report complete
rmdir "$TMPDIR" 2>/dev/null || true
printf '\nReport: %s\nResults: %s\n' "$REPORT" "$RESULTS"
if awk -F '\t' '$1 == "FAIL" { failed=1 } $1 == "BLOCKED" { blocked=1 } END { if (failed) exit 1; if (blocked) exit 2; exit 0 }' "$RESULTS"; then
  exit 0
else
  exit $?
fi
