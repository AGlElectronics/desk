# ADR 0009 — Fetcher, sender and daily jobs run inside one backend replica

Status: accepted (pre-1.0.0).

## Context

Desk polls mailboxes, sends queued replies and notifications, and runs
daily jobs (retention, mailbox purge, reminders). These could be separate
services, or run inside the backend.

## Decision

- All workers run as goroutines in the backend process. The Helm chart
  refuses `backend.replicas` other than 1.
- Even so, the send queue and the notification outbox claim rows with
  `FOR UPDATE SKIP LOCKED` (status `sending`); a claim older than 15
  minutes goes back to the queue. A `QUIT` failure after the server
  accepted `DATA` counts as sent, so a reply is never sent twice.
- On shutdown (SIGTERM) the workers are cancelled and awaited before the
  database pool closes; claimed but unsent rows go back to the queue.
- The daily jobs record their last run in the `settings` table, so a
  restart does not run them twice in a day.

## Consequences

- Simple deployment: no extra Deployment, no leader election.
- Two replicas would poll every mailbox twice. The `Message-ID` dedupe and
  the row claims keep that harmless, but it is not supported. Scaling the
  API later means moving polling behind a per-mailbox advisory lock.
