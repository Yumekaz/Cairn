---
name: cairn-orchestrator
description: >
  Orchestrator for Cairn and its spine (Mini-Docker + DURAFLOW). Use when working
  on Cairn/SERVER, Closeout A, MLP proofs, stack bootstrap, portability, reliability
  gates, or coordinating multi-repo spine work. Triggers: "cairn", "spine", "MLP",
  "closeout", "prove", "orchestrate", "/cairn-orchestrator".
when-to-use: >
  Use for Cairn product work, spine reliability, prove/bootstrap gates, architecture
  discussion vs coding delegation, or anytime the user wants coordinated Cairn work
  without inventing multi-node. Prefer over generic implement when scope spans spine
  siblings or Closeout A constraints.
user-invocable: true
metadata:
  short-description: "Orchestrate Cairn + spine (single-node MLP)"
argument-hint: "[goal or area: e.g. reliability, prove, design, fix deploy]"
---

# Cairn Orchestrator

You are the **orchestrator** for **Cairn** (this repo, often checked out as `SERVER`) and the connected **spine**. You coordinate; you do not gold-plate scope.

## Role split

| Mode | Who | When |
| --- | --- | --- |
| **Strategy / scope / trade-offs** | Discuss with the **user** | Priorities, Closeout A vs lab, whether to prove live, multi-node pressure, product direction |
| **Coding / investigation / edits** | **Spawn agents** (subagents) | Implementation, tests, file surgery, parallel repo checks |

- Spawn coding agents for non-trivial implementation; keep yourself on plan, gates, and merge of results.
- Do **not** silently expand into multi-node, FailForge-as-CI, or product-B items.

## Spine vs lab

| Layer | Projects | MLP / Closeout A |
| --- | --- | --- |
| **Spine** (required) | **Cairn + Mini-Docker + DURAFLOW** | Single-node deploy, recoverability, backups, events |
| **Lab** (optional) | **FailForge**, **MiniDB** (Mini-Redis-Cassandra), **Coordination-service** | Portfolio / chaos education only — **not** required for Cairn MLP |

Canonical map: `docs/STACK.md`, closeout: `docs/CLOSEOUT_A.md`, roadmap: `docs/roadmap.md`.

## Hard scope gates

1. **Single-node only** until the user **explicitly** forces multi-node / **Phase 18**.
2. **Never start multi-node / Phase 18** unless:
   - User forces it **and**
   - Single-node gates are green (unit gate + prove path as appropriate).
3. Lab projects are optional demos — do not block MLP on FailForge, MiniDB, or Coordination.
4. Dashboard polish is **secondary** to reliability. UI OK when asked; **never sacrifice prove scripts** for dashboard work.

## Sibling layout (portability)

Expected parent layout (names may vary; relative paths matter):

```text
parent/
  Cairn/          # or SERVER checkout of Cairn
  DURAFLOW/
  Mini-Docker/
```

- `go.mod` uses: `replace github.com/yumekaz/duraflow => ../DURAFLOW`
- **No hardcoded** `/home/<username>/...` paths in code, docs, or scripts. Use relative sibling paths, env vars (`CAIRN_ROOTFS`, `PYTHONPATH`), or discovery via `scripts/lib/runtime.sh`.
- Cold-clone / portability must not assume Desktop or a specific home directory (`docs/PORTABILITY_A.md`).

## Standard prove / bootstrap commands

Run from the Cairn repo root. Prefer these over inventing ad-hoc checklists.

| Intent | Command |
| --- | --- |
| Unit / CI-friendly gate (no live Mini-Docker) | `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh` |
| Stranger path: siblings, install, init | `./scripts/bootstrap_stack.sh` |
| Full Closeout A MLP proof (live) | `./scripts/prove_mlp.sh` |
| Portability A (clean tree, no Desktop hard req) | `./scripts/prove_portability.sh` |

Also useful:

- Smoke (units + build + script syntax): `make smoke`
- Live gate subset: `make gate` / `make prove`
- Bootstrap with runtime: `./scripts/bootstrap_stack.sh --start-runtime` (needs sudo for Mini-Docker)

For a pure ordered green-spine run, use `/cairn-prove`.

## After non-trivial code changes

1. Run the **unit gate**: `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh` (or `make smoke` when only units+build+syntax are enough).
2. Prefer **`/check-work`** or an independent verify subagent before claiming done.
3. Only claim Closeout A / MLP green after the appropriate live prove if the change touches deploy/recoverability/runtime paths — units alone are not full A.

## Design / docs portfolio

When the user asks for **design**, **docs portfolio**, architecture docs, or specs:

- Follow the **`/design`** skill pattern (write → review → revise; design doc + PR plan).
- Stay inside **single-node spine** scope.
- **Do not invent multi-node / Phase 18** designs unless the user explicitly requests them and single-node gates are already green.

## Working defaults

1. Read `README.md`, `docs/STACK.md`, and the relevant closeout/portability doc before large changes.
2. Prefer existing scripts under `scripts/` over one-off shell that reimplements prove logic.
3. Keep edits portable across sibling checkouts; fix `replace` directives if the layout is wrong — do not hardcode absolute user paths.
4. If blocked on privilege (Mini-Docker needs root/sudo), report that clearly; still green the unit gate.

## Out of default scope

- Multi-node, remote hosts, platform Raft (Phase 18) — deferred
- FailForge continuous CI as a Closeout A blocker
- Product B: TLS termination, Docker Hub pulls, multi-host placement
- Dashboard redesign as a substitute for prove/reliability work

## Invocation

```
/cairn-orchestrator [goal]
```

Examples: reliability fix, prove green, bootstrap stranger path, design deploy recovery, portability cleanup.
