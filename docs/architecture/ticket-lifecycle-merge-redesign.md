# Ticket Lifecycle and Merge Redesign

## Context

Current Dalek semantics mix three different concerns:

- ticket execution lifecycle
- dispatch/bootstrap mechanics
- merge/integration observation

This creates two persistent design problems:

1. `start` and `dispatch` split one business action into two ticket-level commands.
2. merge is modeled as a separate mutable workflow instead of a ticket-owned observed integration state.

This document defines the target semantics for a simpler ticket model.

## Goals

- Make `start` the only PM-visible launch action for ticket execution.
- Demote `dispatch` into an internal bootstrap skill/runtime step.
- Make merge/integration a ticket-owned state instead of an independent queue state machine.
- Use git base-branch reality as the only truth for `merged`.
- Keep PM graph, TUI, and ticket/runtime views consistent.

## Non-goals

- Do not redesign worker runtime internals in this change.
- Do not require git hooks as the source of truth.
- Do not keep merge approval as a separate merge-state concept.

## State Model

### 1. Ticket workflow_status

Ticket execution lifecycle is reduced to:

- `backlog`
- `active`
- `blocked`
- `done`
- `archived`

Semantics:

- `backlog`: ticket exists but has not started execution.
- `active`: worker execution context is running or expected to keep progressing.
- `blocked`: ticket cannot progress without external action or explicit repair.
- `done`: implementation is complete and a stable delivery anchor has been frozen.
- `archived`: terminal cleanup state after delivery has been finalized or abandoned.

`queued` is removed from the PM-visible ticket lifecycle. Queueing remains an internal runtime concern, not a business-visible ticket phase.

### 2. Ticket integration_status

Integration is modeled on the ticket itself:

- empty / not-applicable before `done`
- `needs_merge`
- `merged`
- `abandoned`

Semantics:

- `needs_merge`: ticket implementation is done, but its delivery anchor is not yet observed on the base branch.
- `merged`: the base branch has been observed to contain the ticket delivery anchor.
- `abandoned`: the ticket delivery will not land from this ticket. This is a PM decision, not a branch deletion fact.

`abandoned` applies to the ticket deliverable, not the physical branch. A deleted branch can still correspond to `merged` if equivalent code already landed on the base branch.

### 3. Ticket delivery anchor

When a ticket transitions into `done`, the system must automatically freeze:

- `merge_anchor_sha`
- `target_branch`
- optional audit fields such as `merged_at`, `abandoned_reason`, `superseded_by_ticket_id`

The first implementation should treat `merge_anchor_sha` as the ticket worktree `HEAD` at `done` time.

This anchor must be system-captured, not agent-authored.

## Start / Dispatch Redesign

### PM-visible action

`start` becomes the only PM-visible launch action.

It owns the full business transition:

- ensure / repair worker resource
- ensure worktree exists
- sync worktree baseline to configured base branch
- inject bootstrap context / plan
- start worker execution
- promote ticket into `active` on success

### Internal action

`dispatch` is no longer a top-level ticket semantic.

It becomes:

- an internal bootstrap/runtime step
- or a reusable skill used inside `start`

Compatibility note:

- if the CLI keeps a `ticket dispatch` command temporarily, it should behave as a compatibility shim, not as a separate ticket lifecycle phase.

## Merge / Integration Redesign

### Core rule

Merge is not a mutable queue workflow. It is an observed integration result on the ticket.

The only truth rule is:

- a ticket is `merged` when the base branch is observed to contain `merge_anchor_sha`

### Who performs the merge

Actual integration may be performed by:

- a human
- a PM agent
- an integration ticket / worker

This actor never writes `merged` directly.

State progression happens only through reconcile:

- actor changes git reality
- tick observes base branch reality
- tick appends ticket integration event
- reducer updates `integration_status`

### Abandon flow

If PM decides the ticket deliverable will not land from this ticket:

- `integration_status -> abandoned`
- ticket may later become `archived`

Downstream dependencies must not unlock on `abandoned` unless an explicit replacement mapping exists.

## Tick Detection

The authoritative observation path is a manager tick reconcile step.

Candidate set:

- `workflow_status = done`
- `integration_status = needs_merge`

Per tick:

1. read current `target_branch` head once
2. for each candidate, check whether `merge_anchor_sha` is reachable from the target branch head
3. if yes:
   - append `ticket.merged_observed`
   - set `integration_status = merged`
   - set `merged_at`
   - mark planner dirty
4. if no:
   - keep `needs_merge`
5. if anchor / branch is invalid:
   - raise incident / inbox instead of guessing

Git hooks may be used only to wake the daemon faster. They are not the source of truth.

## Projection to UI and PM Graph

### TUI / dashboard

UI should stop rendering merge as a separate queue object and instead project ticket integration state:

- ticket row shows `workflow_status` + `integration_status`
- dashboard shows integration counts:
  - `needs_merge`
  - `merged`
  - `abandoned`
- the current merge section should become an `awaiting merge` ticket section

### PM graph

PM graph should not perform its own git observation. It should project from ticket state:

- `backlog` -> `pending`
- `active` -> `in_progress`
- `blocked` -> `blocked`
- `done + needs_merge` -> `in_progress` with note `awaiting merge`
- `done + merged` -> `done`
- `done + abandoned` -> `failed` or explicit abandoned rendering, depending on graph vocabulary

This keeps dependency gating correct:

- downstream ticket nodes unlock only after upstream ticket is actually `merged`

## Kernel Semantics to Update

The kernel must reflect the following target semantics:

- ticket lifecycle no longer exposes `queued` / `dispatch` as PM-visible business phases
- `start` owns the full ticket activation action
- merge is a ticket integration state, not an independent mutable state machine
- `merged` is git-observed truth
- `abandoned` is a ticket delivery decision

## Implementation Scope

The implementation ticket for this redesign must cover at least:

- ticket schema / reducer changes for `integration_status`
- automatic anchor freezing on `done`
- manager tick integration reconcile
- dashboard / TUI / PM graph projection updates
- `start` / `dispatch` semantic consolidation
- kernel and state-model consistency updates
