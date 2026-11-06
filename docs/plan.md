# Desk — plan

A shared-inbox helpdesk for the Con Services suite, modelled on FreeScout:
attendees and partners email `support@…`, `registration@…` and so on; each
address is a **mailbox** in Desk; every email thread becomes a
**conversation** (ticket) that staff assign, answer, note and close from the
browser. Customers never log in; to them it is plain email. Same shape as
every other module (own database, own bucket, OIDC against the shared
Keycloak, role catalog, Den pairing, Den's nightly backups).

Status: **planning**. Nothing here is built yet.

## Decisions so far

| Question | Decision |
| --- | --- |
| Model | FreeScout's: mailboxes → conversations → threads (customer message, agent reply, internal note, system line). |
| Email in | IMAP polling per mailbox from the Go backend (`emersion/go-imap/v2`). No separate mail-fetcher service. |
| Email out | SMTP per mailbox (`wneessen/go-mail`). Dev: Mailpit catches it, like HR's emails. |
| Live "who is looking at this" | Polling (a heartbeat every ~10 s), no websocket. Good enough for collision warnings. |
| Storage | Raw `.eml` and attachments in S3; conversations, threads (sanitised HTML + text), customers and metadata in Postgres. |
| Name | Desk — `desk.conservices.test`, Keycloak groups `desk-*`, client `desk-spa`. |

## Stack (same as Learn unless noted)

- **Backend (`desk-backend`):** Go 1.25, chi, pgx, Goose migrations
  (`cmd/migrate` one-shot), go-oidc, zerolog, `minio-go/v7` (Stash's
  `internal/files` client), `emersion/go-imap/v2` + `emersion/go-message`
  for fetching and parsing, `wneessen/go-mail` for sending,
  `microcosm-cc/bluemonday` for sanitising incoming HTML.
- **Frontend (`desk-frontend`):** React 19 + Rsbuild + TanStack
  Router/Query + Tailwind, the same Layout, brand, theme, login backdrop and
  Den-connection components as Learn. Reply editor: BlockNote (same editor
  as the wiki, no live editing) exported to HTML + plain text on send.
  Incoming email bodies render in a sandboxed `iframe` (no scripts, remote
  images blocked until "Show images" is clicked).

## How email flows

**Fetching (every 60 s per mailbox, one goroutine each):**

1. Connect over IMAPS (plain IMAP is allowed per mailbox, for GreenMail in
   dev), select the inbox, read `UIDVALIDITY` and fetch every
   UID above the last one stored for that mailbox.
2. For each message: store the raw bytes in S3, parse it, drop it if its
   `Message-ID` was already imported (re-fetch after a crash is safe).
3. Find the conversation it belongs to, in this order:
   - `In-Reply-To` / `References` match a `Message-ID` Desk sent or
     imported (the normal case: the customer pressed Reply);
   - the subject contains `[#<number>]` for a conversation in this mailbox
     (Desk adds it to outgoing subjects; catches clients that drop headers);
   - otherwise it is a **new conversation**.
4. Attachments and inline images go to S3; `cid:` references in the HTML
   are rewritten to Desk's download URLs; the HTML is sanitised and a
   plain-text version is kept for search.
5. Customer is found or created by sender address. A reply to a closed
   conversation reopens it (status back to `active`).
6. Mark the message `\Seen` on the server and store the new last UID. If
   `UIDVALIDITY` changed, re-scan and rely on the `Message-ID` dedupe.

**Sending (agent reply):**

1. The reply is saved as a thread with status `queued`, then a worker sends
   it over the mailbox's SMTP with `From: <Mailbox name> <address>`,
   `Message-ID: <desk.<thread_id>@<mailbox domain>>`, and `In-Reply-To` /
   `References` set from the conversation, so the customer's mail client
   threads it and their answer comes back to the right conversation.
2. Failures retry with backoff (1, 5, 15, 60 minutes); after the last try
   the thread shows "Not sent" with a Retry button.
3. "Undo send" for ~10 seconds: the worker waits that long before sending.

**Loops and noise:**

- Auto-reply on new conversations (per mailbox, optional), never to
  messages with `Auto-Submitted`, `Precedence: bulk/list/junk`, a
  `List-Id`, or from `MAILER-DAEMON`/`noreply` addresses, and at most once
  per customer per hour.
- Bounces (`multipart/report`) are attached to the conversation they bounce
  from as a system line, not opened as new conversations.
- A message whose sender is one of Desk's own mailboxes is skipped.

**Credentials:** IMAP/SMTP passwords are encrypted in Postgres with
AES-GCM using `DESK_SECRET_KEY` (32 bytes, from env / Kubernetes secret).
Lose the key and the mailbox passwords must be re-entered; nothing else is
affected. OAuth2 (Microsoft 365, Gmail) is out of the first slice.

## Storage: what lives where

| Data | Where | Why |
| --- | --- | --- |
| Raw incoming email | **S3** `desk/raw/<mailbox_id>/<yyyy-mm>/<uuid>.eml` | Exact original for re-parsing and disputes. |
| Attachments, inline images | **S3** `desk/files/<conversation_id>/<uuid><ext>` | Same as Stash. |
| Mailboxes, conversations, threads (sanitised HTML + text), customers, tags, saved replies, assignments | **Postgres** | Needs transactions, filters and counts per folder. |
| Search text | **Postgres** (`tsvector` on conversation subject + thread text + customer email) | Full-text search. |

Bucket `desk-data-dev` goes into the operator's `FILES_S3_BUCKETS` so Den
backs up email and attachments.

## Access model

Module roles (in the role catalog so HR can assign them):

| Keycloak group | Role | May do |
| --- | --- | --- |
| `desk-admin` | `organization_admin` | Every mailbox and conversation, create/edit mailboxes (addresses, IMAP/SMTP), Den pairing, delete forever. |
| `desk-agent` | `agent` | Can be given mailboxes. No mailbox access by itself. |

Mailbox access, stored in Desk (FreeScout's "mailbox permissions"):

- A mailbox has grants to people or Keycloak groups (e.g. `dept-registration`
  from HR departments) with a level: `agent` (read, reply, note, assign,
  change status, tag) or `manager` (also edit the mailbox's signature,
  auto-reply, saved replies and grants).
- Grants only take effect for people who also hold the `agent` role (or
  `organization_admin`), so HR's role assignment stays the master switch.
- Group grants use the `groups` claim of the token on every request, like
  every other module. Conversations in mailboxes you cannot see return 404.

## Conversations

- **Status:** `active`, `pending` (waiting on the customer), `closed`,
  `spam`. Plus **assignee** (one person or unassigned) and **tags**.
- **Folders per mailbox** (as in FreeScout): Unassigned, Mine, Assigned,
  Closed, Spam, plus Starred (per person). Counts in the sidebar.
- **Threads:** customer message, agent reply, internal note (never emailed,
  yellow), system line ("Alice assigned to Bob", "Status → closed").
- **Collision warning:** while a conversation is open the browser sends a
  heartbeat (`viewing` or `replying`) every ~10 s; others see "Alice is
  replying" and get a warning if a new thread arrived while they typed.
- **Actions:** reply, note, forward (to an outside address), assign,
  status, tag, star, merge two conversations, move to another mailbox,
  new conversation (agent starts one to a customer), delete (to trash,
  admins empty it).
- **Customers:** name, email addresses (a customer can have several),
  notes, list of their past conversations on the side of every ticket.
- **Saved replies** per mailbox with `{customer.first_name}`-style
  placeholders; **signature** per mailbox (with `{agent.name}`).
- **Notifications:** email to the assignee on new reply / assignment, and to
  everyone with access on new conversations (each person can turn these
  off per mailbox). Sent through the module's `SMTP_*` like HR's.

## Schema sketch (first migration)

```text
organizations, users (issuer+subject), den_link    -- same as Learn
mailboxes      (id, org, name, email, slug, imap_host, imap_port, imap_user,
                imap_password_enc, smtp_host, smtp_port, smtp_user,
                smtp_password_enc, smtp_security, signature_html,
                auto_reply_enabled, auto_reply_subject, auto_reply_html,
                last_uid, uid_validity, last_fetch_at, last_fetch_error,
                created_at, deleted_at)
mailbox_grants (mailbox, principal_kind user|group, principal, level agent|manager)
customers      (id, org, first_name, last_name, notes, created_at)
customer_emails (email PK citext, customer)
conversations  (id, number per mailbox, mailbox, subject, customer, status,
                assignee_user_id, last_thread_at, last_customer_at,
                created_by_user_id null, merged_into null, created_at,
                closed_at, deleted_at, tsv tsvector)
threads        (id, conversation, kind customer|reply|note|forward|system,
                author_user_id null, customer null, body_html, body_text,
                to/cc/bcc text[], message_id, in_reply_to,
                raw_storage_key null, send_status queued|sent|failed null,
                send_attempts, created_at)
attachments    (id, thread, storage_key, filename, content_type, size_bytes,
                content_id null, inline bool)
tags (id, org, name, color), conversation_tags (conversation, tag)
stars          (user, conversation)
saved_replies  (id, mailbox, name, body_html, position)
viewers        (conversation, user, mode viewing|replying, seen_at)  -- heartbeat, rows older than 30 s ignored
notification_prefs (user, mailbox, on_new, on_assigned, on_reply)
message_ids    (message_id PK, conversation)        -- threading lookup, sent and received
```

## API (backend, first slice)

- `GET /api/me`, `GET /api/role-catalog`, `GET/POST/DELETE /api/den-link`,
  brand — same as Learn.
- Mailboxes: list (only granted), create/update (admins), test connection
  (IMAP login + SMTP `NOOP`), grants, folder counts, "fetch now".
- Conversations: list by mailbox + folder with filters (status, assignee,
  tag, search), get with threads, create, reply, note, forward, assign,
  status, tags, star, merge, move, delete, restore.
- Viewers: `POST /api/conversations/{id}/viewing` (heartbeat), returned in
  the conversation payload.
- Customers: search, get with history, update, merge two customers.
- Saved replies, tags, notification prefs.
- Files: multipart upload for reply attachments (Stash's approach),
  download through the backend after the mailbox access check. 25 MB per
  file, like Stash.
- Search: `GET /api/search?q=` across every mailbox you can see.

## Frontend

- Left sidebar: mailboxes you can see, each with its folders and counts
  (FreeScout layout), plus "Mine across all mailboxes" and search.
- Conversation list: customer, subject + preview, tags, assignee, last
  activity, bulk select (assign, status, tag, delete).
- Conversation view: threads newest-first or oldest-first (setting),
  reply/note editor at the top, saved-reply picker, attachments,
  status/assignee/tags in a toolbar, customer card + their past
  conversations on the right, collision banner.
- Mailbox settings (managers/admins): connection, signature, auto-reply,
  saved replies, people with access.
- Keyboard shortcuts for the common actions (`r` reply, `n` note, `a`
  assign, `c` close, `j/k` next/previous).
- Brand, light/dark mode and sign-in backdrop from Den, like every module.

## Suite wiring

| Thing | Value |
| --- | --- |
| Directory | `desk/` (`backend/`, `frontend/`, `infra/`, `docs/`), same layout as Learn |
| Compose profile | `desk` (`desk-migrate`, `desk-backend`, `desk-frontend`) |
| Host ports | API **8150**, UI **3008**, standalone Postgres **5440** |
| Database | `desk` / user `desk` (add to `infra/postgres/01-init-databases.sh`) |
| Bucket | `desk-data-dev` (add to `minio-init` and to the operator's `FILES_S3_BUCKETS`) |
| Keycloak | client `desk-spa`, groups `desk-admin`, `desk-agent` in `realm-conservices.json` and `apply-mvp.sh`; give `admin` `desk-admin` |
| Proxy | `desk.conservices.test` in the Caddyfile, network alias, hosts entry in `scripts/setup-local-dns.ps1` |
| HR | add `desk=http://desk-backend:8080/api/role-catalog` to `ROLE_CATALOG_SOURCES` |
| Den | pairing (Settings → Den connection), same as Learn |
| Secrets | `DESK_SECRET_KEY` (mailbox password encryption) |
| Dev mail server | **GreenMail** — already in the root `docker-compose.yml` (service `greenmail`, profile `desk`, dev only like Mailpit). In the network: IMAP `greenmail:3143`, SMTP `greenmail:3025`; on the host 3143 / 3025, API on 8096. Mailboxes `support@conservices.test` / `support` and `registration@conservices.test` / `registration`. The smoke script sends "customer" mail into it and Desk fetches from it; Desk's replies go out through Mailpit. Mail is in memory only. |
| Smoke | `scripts/smoke-desk.ps1` |
| README | root table rows + a Desk note, `desk/README.md` like Learn's |

Deployment: **one replica** for the fetchers (two pods would fetch the same
mailbox twice; the `Message-ID` dedupe keeps that harmless but noisy). If
the API ever needs more replicas, move fetching behind a Postgres advisory
lock per mailbox. `strategy: Recreate`, no volume.

## Milestones

1. **Scaffold.** Copy Learn's skeleton into `desk/`, add Stash's S3 client,
   wire everything in the table above including GreenMail. Done when the
   empty app signs in, pairs with Den and appears in HR's access picker.
2. **Mailboxes and access.** Create mailboxes (encrypted credentials,
   connection test), grants, folder skeleton, authorization tests against
   a real Postgres (Learn's `TEST_DATABASE_URL` pattern).
3. **Inbound.** IMAP fetcher, parser, threading rules, attachments to S3,
   sanitising, customers. Tests with a folder of real-world `.eml` fixtures
   (Outlook, Gmail, Apple Mail, forwarded, bounces, auto-replies, non-UTF-8).
   Done when mail sent to GreenMail shows up as conversations and replies
   land in the right one.
4. **Working tickets.** List and conversation views, reply (SMTP, queue,
   retry, undo), notes, assign, status, tags, stars, folder counts,
   collision banner.
5. **Mailbox niceties.** Signatures, auto-reply with loop protection, saved
   replies, notifications, forward, merge, move, trash.
6. **Search and customers.** Full-text search, customer profiles and
   history, customer merge, keyboard shortcuts.
7. **Smoke script and docs.** `scripts/smoke-desk.ps1` (create mailbox →
   send mail into GreenMail → conversation appears → reply → reply arrives
   in Mailpit with the right headers → customer answers → same
   conversation reopens → revoke access → 404), READMEs, operations notes.

## Out of the first slice

- Customer portal / web contact form (could use the attendees realm and
  open conversations without email).
- OAuth2 mailboxes (Microsoft 365, Gmail), IMAP IDLE instead of polling.
- Workflows / automation rules ("if subject contains X, assign to Y"),
  SLAs and reporting, satisfaction ratings.
- Live chat widget.
- Linking a conversation to a Rally attendee or creating a task in Tasks
  from a conversation (see `tasks/docs/plan.md`).
- Multiple languages for auto-replies.

## Open questions

- Which real mailboxes first, and where are they hosted (own mail server,
  Microsoft 365, Gmail)? Microsoft 365 and Gmail increasingly refuse IMAP
  passwords, which would pull OAuth2 into the first slice.
- Should Desk delete or move fetched mail on the server (FreeScout can), or
  only mark it read?
- Retention: how long to keep closed conversations and raw `.eml` (GDPR —
  attendee emails contain personal data)?
- Should mailbox access be a role per mailbox in HR's picker (one Keycloak
  group per mailbox) instead of grants inside Desk?
