# Remediation restart recovery

The API starts a recovery worker after wiring the remediation coordinator. It scans immediately and then every `MENDRY_REMEDIATION_RECOVERY_INTERVAL` (default `15s`, range `1s`–`5m`). No new database migration is required for this worker; the existing remediation/checkpoint schema must already be migrated.

Normal starts, manual continuations, plan execution, and recovery share the same per-run PostgreSQL session advisory lock. The lock covers the entire execution, including model calls and lifecycle effects. If another process still owns a run, recovery skips it. When a process exits and PostgreSQL closes its session, a later scan can acquire the run. This is session ownership, not an inactivity timeout: a long model call does not cause another worker to take over.

`MENDRY_REMEDIATION_CONCURRENCY` limits normal and recovered executions together (default `4`, range `1`–`32`, per API process). Work that cannot acquire local execution capacity remains durable in `queued` and is picked up by a later scan. Each active execution holds one additional PostgreSQL connection outside the query pool, so reserve that many connections in addition to `MENDRY_POSTGRES_MAX_CONNS`. Use a direct PostgreSQL connection or a session-pooling proxy; transaction-pooling proxies do not preserve session advisory locks.

The execution session is checked every five seconds with a three-second timeout. Run state, checkpoint, and effect persistence inside an owned execution use that same locked session; after the session is lost, stale writes fail instead of falling back to the query pool. A lost session also cancels model/tool operations when detected. An external request already in flight can still complete remotely, so lifecycle effects retain their existing durable idempotency keys. On shutdown the executor stops admission, cancels work, and waits up to `MENDRY_SHUTDOWN_TIMEOUT` for execution locks to be released before database clients close. Shutdown cancellation preserves the last durable active state instead of recording a terminal business failure, including cancellation during terminal checkpoint persistence. An interrupted model request may be repeated.

## What resumes

- Queued roots start normally, including roots committed before their original trigger could run.
- Queued continuations reconstruct the predecessor diagnosis, evidence, and planning entry point without creating another attempt. A continuation claimed just before its first checkpoint is reconstructed the same way.
- Active `resilient_v1` runs resume diagnosis, evidence collection, planning, patching, validation, or publication from durable checkpoints and effects. Saved budgets and policy snapshots are retained.
- Review and terminal states are not advanced automatically. Obsolete generations/baselines and non-latest attempts are excluded from scans.
- Active legacy runs have no resumable working-memory contract. They are marked `failed` with `terminalReason=recovery_checkpoint_unavailable`, making the existing manual continuation action available. Recovery does not silently rerun their external actions.

A recoverable run whose checkpoint or required adapter cannot be loaded stays durable and is retried with delays of one, two, four, then five minutes. Backoff is local to the process and resets after restart. The worker logs `remediation.recovery` with `run_id`, `outcome`, and a fixed `reason` code; it does not log model contents or credentials.

## Rollout and diagnosis

Stop old API binaries before the first rollout of this worker. Older binaries do not hold execution locks, so a mixed deployment cannot distinguish their live tasks from orphaned tasks. Once every process uses the shared executor, multiple API instances can recover tasks without concurrently driving the same healthy run. Lifecycle workspace and artifact storage must also remain available to whichever instance resumes a lifecycle run.

Restarting the new API enables recovery with the defaults above. This change does not restart a running development server or modify existing project policies. The incident UI currently does not poll remediation status; refresh the page to see recovery progress. Look at the run's saved `agentLoopMode` rather than the project's current policy to determine whether an old active run can resume.

Tests cover immediate startup scans, pagination past busy runs, retry backoff, cross-executor exclusion, session-loss/shutdown cancellation, same-attempt checkpoint restart, continuation reconstruction, and reuse of durable publication results. PostgreSQL integration tests additionally exercise real advisory-lock exclusion and database-session termination; they require a reachable migrated test database.
