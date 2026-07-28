---
name: cairn-prove
description: >
  Run Cairn spine prove scripts in order for a green single-node check.
  Use when asked to prove, gate, green the spine, Closeout A, MLP proof,
  portability proof, or "/cairn-prove".
when-to-use: >
  Use when the user wants a green spine check, MLP/Closeout A proof, unit gate,
  bootstrap, or portability verification. Not for multi-node or Phase 18.
user-invocable: true
metadata:
  short-description: "Ordered prove scripts for green Cairn spine"
argument-hint: "[unit|bootstrap|mlp|portability|full]"
---

# Cairn Prove

Run prove scripts **in order** for a green **single-node spine** check. Do not invent multi-node proofs. FailForge/lab are out of scope.

Work from the **Cairn repo root** (this workspace). Require sibling `../DURAFLOW` and `../Mini-Docker` for live steps (or envs discovered by `scripts/lib/runtime.sh`).

## Ordered spine check

Default **`full`** (or no arg): run 1 → 4. Stop on first non-zero exit.

| # | Step | Command | Needs |
| --- | --- | --- | --- |
| 1 | Unit gate | `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh` | Go only |
| 2 | Bootstrap | `./scripts/bootstrap_stack.sh` | Siblings; no live runtime required |
| 3 | MLP proof | `./scripts/prove_mlp.sh` | Privileged Mini-Docker + DURAFLOW |
| 4 | Portability | `./scripts/prove_portability.sh` | Clean temp sibling tree |

### Partial modes

| Arg | Runs |
| --- | --- |
| `unit` | Step 1 only |
| `bootstrap` | Steps 1–2 |
| `mlp` | Steps 1 then 3 (skip bootstrap if stack already set up) |
| `portability` | Step 4 only (still recommend step 1 first if code changed) |
| `full` | Steps 1–4 |

## How to run

1. `cd` to Cairn root; ensure scripts are executable (`chmod +x scripts/*.sh scripts/lib/*.sh` if needed).
2. Execute the selected sequence; capture exit codes and last ~50 lines of failure output.
3. Report: which steps passed/failed, log paths if scripts write under `/tmp/cairn-proof-runs` (or `LOG_DIR`), and whether live privilege blocked steps 3–4.
4. Do **not** claim Closeout A green unless step 3 (MLP) exits 0.

## Notes

- Quick MLP (skip F2): `PROVE_QUICK=1 ./scripts/prove_mlp.sh`
- Live demos only (rare): `SKIP_UNIT=1 ./scripts/prove_mlp.sh` — prefer full unit gate first after code changes
- Bootstrap + start Mini-Docker: `./scripts/bootstrap_stack.sh --start-runtime` (sudo)
- Alternatives: `make smoke` ≈ light unit/build; `make prove` → `prove_mlp.sh`

## Out of scope

Multi-node / Phase 18, FailForge campaigns, dashboard-only checks.
