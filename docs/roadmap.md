# Cairn Roadmap

This document outlines the upcoming phases and planned architecture features for Cairn.

---

## 🔬 Phase 17: FailForge Integration & Failure Testing

**Goal**: Use systematic failure testing to validate and strengthen platform correctness.

FailForge is a planned testing framework designed to simulate infrastructure failures and verify that Cairn recovers gracefully:

- **Daemon Crashes**: Terminate `cairnd` mid-deploy and verify DuraFlow resumes safely.
- **Runtime Failures**: Kill `mini_docker` or simulate network/socket timeout conditions during volume operations.
- **Service Crash Loops**: Test daemon behavior when an application container fails on startup.
- **Backup & Restore Interruptions**: Unplug volume storage during logical database dumps to assert checksum safety.
- **Network Delays**: Simulate latencies between bridge network containers to check API retry policies.
- **Database Corruption**: Inject bad data into SQLite metadata to verify recovery from hourly snapshots.

---

## 🌐 Phase 18: Optional Multi-Node / Advanced Mode

**Goal**: Explore distributed orchestration after single-node stability is fully achieved.

> [!WARNING]
> **Strict Guideline**: Do not build multi-node primitives before single-node operations are mature. Rushing to distributed systems too early introduces unnecessary orchestration bloat.

### Planned Advanced Features
- **Remote Deploy Targets**: Allow a local `cairn` CLI to push and configure services on remote hosts.
- **Coordinator Service**: Introduce a lightweight control plane manager to balance workloads.
- **Replicated Metadata Store**: Swap SQLite out for a replicated consensus-based key-value store (e.g. Raft) when scaling.
- **External Backup Targets**: Allow archiving volume backups directly to S3-compatible object storage.
- **Service Placement Rules**: Define server labels and schedule database workloads on dedicated stateful nodes.
