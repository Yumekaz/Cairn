# Validation record — September 2026

## Cairn dashboard flow

Tested against a live local Cairn daemon using the persisted
`notes-proof-62a235f5` test service and its dedicated data volume.

| Flow | Result |
| --- | --- |
| Overview, Services, Volumes & Backups, Events navigation | Passed |
| Service listing and inspect modal | Passed |
| Service details, deploy history, lifecycle state, live logs | Passed |
| Start and restart actions | Passed; state and logs refreshed |
| Volume inspection and backup creation | Passed |
| Restore confirmation, disabled Proceed, and cancellation | Passed |
| Events timeline and recent audit records | Passed |
| Daemon offline banner and disabled destructive controls | Passed |
| Daemon restart and Retry now reconnect | Passed |

The restore Proceed action was not clicked through the browser because it
replaces volume contents; the same restore API is covered by the notes smoke
workflow and Cairn backup/restore tests. It requires deliberate operator action.
The service-search field, log Wrap/Clear/Follow controls, event type filter,
rollback confirmation, and Restore Proceed were not all exercised in the
retained browser trace. The dashboard was unreachable when browser validation
was resumed on September 21; these controls remain an open acceptance gate.

The runtime helper now launches `cairnd` in a new session where `setsid` is
available. This was verified by starting the helper, allowing its command to
return, then checking the daemon from a separate command.

## Cross-project results

| Project | Validation |
| --- | --- |
| Cairn | internal Go tests, live deployment/recovery matrix, backup/restore, migration replay guard, dashboard flows |
| DuraFlow | `go test ./...` passed |
| Mini-Docker | 88 normal tests plus 5 guarded root runtime tests passed after preventing character-device copies from filling `/tmp` |
| FailForge | `go test ./...` passed after fault-error handling; CPU pause and slow disk repeated 25 times |
| MiniDB | 7 cluster tests plus 23 other tests passed after correcting RF=1 single-node expectations and checking follower reads |
| Coordination-service | 311 tests passed, including leadership transfer/removal |

## Limits

This record covers reproducible software failures on this host. It does not
claim validation of a physical power loss, an actual host reboot, long-term
production load, arbitrary hostile containers, or every possible Linux kernel
and filesystem combination. Those require disposable hosts and longer-running
operational observation.

## Remaining acceptance gates

1. Start `cairnd` on a dedicated, disposable Linux host and verify service
   delivery after an actual host reboot. The current Desktop host was not
   rebooted as part of this run.
2. Run a bounded multi-day soak with recorded request success, restart count,
   backup restore verification and resource usage. The current smoke runs were
   measured in minutes, not days.
3. Test disk-full and host-level power interruption in a disposable filesystem
   or VM. The in-process SIGKILL tests do not simulate a power cut.
4. Complete a hostile-workload isolation review and independent security test
   before accepting untrusted tenants.
5. Finish browser checks for search, log controls, event type filters and
   confirmed restore/rollback against disposable data. The restore confirmation
   UI disabled Proceed until acknowledgement and Cancel worked, but the
   destructive final action was not performed through the browser.
