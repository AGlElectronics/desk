# ADR 0003 — Access is three roles per mailbox, published in the role catalog

Status: accepted (pre-1.0.0, maintainer decision). Replaces the MVP's
organization-wide `agent` role and the grants-inside-Desk design in
`docs/plan.md`.

## Context

In the MVP every agent saw every mailbox. Conventions need "registration
staff see registration@, not press@". Two designs were on the table: grants
stored inside Desk, or roles granted through Keycloak groups like every
other module. HR already assigns module roles by adding people to groups,
and fetches each module's role catalog live.

## Decision

- Every mailbox has an immutable **slug** (default: the address' local
  part, `lost.found@` → `lost-found`) and publishes three roles in Desk's
  role catalog (`GET /api/role-catalog`), each granted by a Keycloak group:

  | Role key | Group | May |
  | --- | --- | --- |
  | `desk_<slug>_viewer` | `desk-<slug>-viewer` | Read the mailbox's conversations. |
  | `desk_<slug>_manager` | `desk-<slug>-manager` | Also reply, note, assign, close, tag, use and edit saved replies, change signature and auto-reply, start conversations with any address. |
  | `desk_<slug>_delete` | `desk-<slug>-delete` | Delete and restore emails and conversations (Desk's trash and the provider's). |

  The roles are independent: someone who should delete and answer needs
  both `manager` and `delete`.
- **Desk admin** is the module-wide role `organization_admin` (group
  `desk-admin`, permission `mailbox.manage`). It is checked against the
  mailbox's organization and allows everything there: create, configure,
  delete and purge mailboxes, every mailbox's conversations, retention and
  the audit log.
- Checks are per request from the token's groups, so removing someone from
  a group revokes access on their next request.
- A mailbox or conversation the caller may not view answers **404**; one
  they may view but not change answers **403** naming the missing role.
- Agents get a summary of a mailbox (no hosts, usernames or fetch errors);
  Desk admins get the full configuration.
- The resolver stores each user's groups and admin flag at sign-in, so
  background jobs (assignee picker, notifications) can tell who may work a
  mailbox without a token.

## Consequences

- HR's access picker shows a new mailbox's roles as soon as it exists; no
  Desk-side grant UI is needed.
- One Keycloak group per mailbox and level. Keycloak creates groups on
  demand when HR grants a role.
- **BREAKING** against the MVP: `desk-agent` and `tickets.work` are gone.
  Grant `desk-<slug>-viewer`/`-manager`/`-delete` instead.
- The slug cannot be renamed, because it names groups people are in.
