# Conductor job/run state model

Package: [`internal/conductor`](../../internal/conductor)

This document describes the durable domain model Conductor uses to
supervise work, implemented in `internal/conductor`. It resolves
[grove-project/dev-factory#2](https://github.com/grove-project/dev-factory/issues/2).

## Why this exists

Conductor supervises long-running, externally-executed work (today: Claude
Code cloud runs acting on GitHub issues) that can pause, stall, get blocked,
or have its execution resource disappear entirely without the underlying
unit of work being finished or abandoned. This package gives every other
subsystem (execution, GitHub observation, scheduling, operations) a single,
GitHub/Claude-agnostic vocabulary for that supervision, so none of them need
to understand each other's implementation details — they only need to
understand `Job`, `Run`, `Lease`, `Session`, `Branch`, and the `Store`
interface.

## Concepts

| Type      | What it represents                                                             | Lifetime |
|-----------|----------------------------------------------------------------------------------|----------|
| `Job`     | The desired unit of work (normally a GitHub issue), referenced via an opaque `SourceRef`. | Long-lived; never deleted. |
| `Run`     | One execution attempt at a `Job`.                                              | Long-lived; never deleted, even once terminal. |
| `Lease`   | Permission for exactly one `Run` to be the active executor of a `Job`.          | Active until released. |
| `Session` | A disposable external agent execution resource (e.g. a Claude Code cloud run). | Disposable; can fail independently of everything else. |
| `Branch`  | The isolated Git branch a `Run`'s output durably lives on.                     | Permanent, globally unique name. |
| `Affinity`| Workstream + context-affinity metadata on a `Job` (e.g. "Core Runtime", "keep with the same agent"). | Attached to the `Job`. |

A `Job` can have many `Run`s over time (retries, restarts after a stalled
attempt, etc.), but at most one `Lease` is active for a `Job` at any moment,
and that lease names the one `Run` currently allowed to act.

## States

Both `Job` and `Run` use the same `State` vocabulary, defined once in
`state.go`:

```
READY → ACTIVE → WAITING ⇄ BLOCKED ⇄ STALLED → DONE / FAILED / SUPERSEDED
  └──────────────────────────────────────────→ SUPERSEDED
```

- **READY** — defined, no active executor yet.
- **ACTIVE** — a lease/session is live and work is progressing.
- **WAITING** — paused on an expected external event (review, CI, etc.).
- **BLOCKED** — cannot proceed without an intervention.
- **STALLED** — no forward progress observed for longer than expected.
- **DONE** — terminal success.
- **FAILED** — terminal failure.
- **SUPERSEDED** — terminal; a newer `Run`/`Job` made this one obsolete.

`DONE`, `FAILED` and `SUPERSEDED` are terminal: no further transitions are
allowed out of them (`State.Terminal()`). `ACTIVE`, `WAITING`, `BLOCKED` and
`STALLED` freely move among each other, reflecting that the GitHub Observer
subsystem (issues #9–#11) continuously reassesses a live run's progress.

`Job` and `Run` each have their own transition table (`jobTransitions`,
`runTransitions` in `state.go`). They happen to be identical today, but they
are validated independently on purpose: a `Job`'s state is a policy decision
about the unit of work as a whole (e.g. "superseded" because the tracked
issue was closed as a duplicate), while a `Run`'s state is a fact about one
execution attempt. Keeping them separate lets one diverge from the other
without a breaking change.

Transitions are validated, not just performed, by `TransitionJob(from, to)`
and `TransitionRun(from, to)`; both return `ErrInvalidTransition` (checkable
via `errors.Is`) for any move not in the table, including no-op self-loops
and unknown state values.

## Invariants and where they're enforced

| Invariant | Enforcement |
|---|---|
| `Session != Job` | Structural: `Session` has no `JobID` field, only `RunID`. It cannot express Job identity. |
| A `Job` survives `Session` failure | `Store.CloseSession` only ever mutates the `Session` row. Turning a session failure into a `Run`/`Lease` outcome requires an explicit, separately-validated `TransitionRun` call by the caller. |
| At most one active lease per `Job` | `Store.AcquireLease` scans for any other active (`ReleasedAt == nil`) lease on the same `JobID` and fails with `ErrLeaseHeld` if one exists. |
| Historical/superseded runs remain traceable | `Store` never deletes `Run`s. Superseding a run sets `State = SUPERSEDED` and `SupersededBy = <new run ID>` via `UpdateRun`; `ListRunsByJob` always returns the full history. |
| Every run has a unique identity and branch | `Run.Validate()` rejects an empty `Branch`; `Store.CreateRun` rejects a duplicate `RunID`; `Store.RegisterBranch` rejects a duplicate branch name (`ErrAlreadyExists`), matching Git's own uniqueness constraint. |

Each of these is covered by a dedicated test in `invariants_test.go`.

## Persistence boundary

`Store` (in `store.go`) is the only interface other subsystems should
depend on. It is intentionally storage-agnostic: `MemStore` (`memstore.go`)
is a thread-safe in-memory implementation good enough for early bring-up
and tests. A durable backend (Postgres, etc.) can implement the same
interface later without changing any consumer.

`Store` methods persist entities as given; they do **not** re-derive or
silently correct state transitions. Callers are expected to call
`TransitionJob`/`TransitionRun` to validate a move *before* constructing the
updated struct and calling `UpdateJob`/`UpdateRun`. This keeps transition
policy in one place (`state.go`) while keeping the storage layer simple.

## How to consume this model

Other subsystems (Execution, GitHub Observer, Scheduling, Operations)
should:

1. Import `internal/conductor` and program against `Job`, `Run`, `Lease`,
   `Session`, `Branch`, and `Store` only.
2. Never reach into GitHub or Claude Code specifics from this package —
   external identifiers belong in `Job.SourceRef` / `Session.ExternalRef`
   as opaque strings that this package never parses.
3. Validate every state change with `TransitionJob`/`TransitionRun` before
   persisting it.
4. Treat `Run`/`Lease` history as append-only: supersede, don't delete.

This is the contract the acceptance criteria for
[grove-project/dev-factory#2](https://github.com/grove-project/dev-factory/issues/2)
asks for: another subsystem can consume this model without needing to
understand Claude/GitHub implementation details.
