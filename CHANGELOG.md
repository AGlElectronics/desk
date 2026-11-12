# Changelog

All notable changes to Desk are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [1.0.0] - 2026-11-12

Needs Den: 1.0.0 or later (Den MAJOR 1).

First release, `1.0.0`. Released versions are tagged `vMAJOR.MINOR.PATCH`;
the images `ghcr.io/aglelectronics/desk-backend`, `desk-frontend` and
`desk-migrate` are published as `MAJOR.MINOR.PATCH-BUILD`, where `BUILD`
counts rebuilds of the same version. Rebuilds get no entry here.

### Added

- Den portal to-dos: `GET /api/portal/todos`. Den's backend calls it with the pairing credential and `X-Den-User-Issuer` / `X-Den-User-Subject`. The answer is the open conversations assigned to that person. Not paired: 404. Wrong credential: 401. The chart admits `den-backend` from `conservices-den`.
- Den link uses the shared client: `DEN_LINK_ENCRYPTION_KEY` (and `_PREVIOUS` while rotating) encrypts the stored credential, and `den_link.revoked_at` records a rejected credential. The Den connection card then says to pair again. SMTP and retention come from Den's module settings (`smtp.*`, `desk.retention.*`); when Den holds the SMTP password, Desk fetches it when sending and does not store it.
- Per-mailbox roles are matched with `OIDC_GROUP_MATCH`, `OIDC_GROUP_PREFIX` and only the claim `OIDC_GROUPS_CLAIM`. Sign-in stores each group normalised and as the token sent it.
- JSON bodies are limited to 1 MiB. New conversations, replies, forwards, mailbox signatures and saved replies may be up to 20 MiB. Oversized bodies answer `413 request_too_large`.
- The web app's nginx sends the shared security headers only on the pages it serves, not on `/api/`. Inline scripts moved to `theme-init.js` and `silent-callback.js`. `/silent-callback.html` is the one page the app may frame (silent sign-in). Customer mail stays in a sandboxed frame: set `EXTRA_FRAME_SRC=about:srcdoc` on the frontend (the image, Compose and the chart do).

- Shared-inbox helpdesk: each support address is a mailbox, and every email
  thread becomes a conversation that staff assign, answer, annotate and
  close in the browser. Customers never sign in; to them it is plain email.
- Mailboxes in **Google Workspace**. A Desk admin creates the mailbox in
  Desk and Desk creates it in Google, either as its own licensed user
  (connector `google_user`) or as a free group delivered into one shared
  account and routed by recipient (`google_group`, replies sent "send as"
  the group address). Needs a service account with domain-wide delegation;
  see [Google Workspace setup](https://aglelectronics.github.io/con-services/modules/desk/google-workspace/).
  New settings `GOOGLE_WORKSPACE_DOMAIN`, `GOOGLE_WORKSPACE_CREDENTIALS_FILE`,
  `GOOGLE_WORKSPACE_ADMIN_EMAIL`, `GOOGLE_SHARED_MAILBOX` and `GOOGLE_LABEL`
  (default `Desk`). Mail Desk has handled gets the Gmail label `Desk`.
- **IMAP/SMTP** connector for mail servers outside Google: IMAP polling and
  SMTP per mailbox (or the module-wide `SMTP_*` server), a connection test
  that changes nothing, mail marked `\Seen` after import and an optional
  "move to folder". See [IMAP setup](https://aglelectronics.github.io/con-services/modules/desk/imap/).
- **Roles per mailbox.** Every mailbox publishes three roles in the role
  catalog, granted by Keycloak groups: `desk-<slug>-viewer` (read),
  `desk-<slug>-manager` (reply, note, assign, close, tag, saved replies,
  signature, auto-reply) and `desk-<slug>-delete` (delete and restore).
  `desk-admin` is the Desk admin role for every mailbox, mailbox setup,
  retention and the audit log. See
  [Access](https://aglelectronics.github.io/con-services/modules/desk/access/).
- Attachments, inline images and the raw `.eml` of every message are stored
  in the module's S3 bucket (`desk-data`); add it to Den's
  `FILES_S3_BUCKETS` so backups include it. New required settings
  `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`,
  optional `S3_REGION` and `S3_USE_PATH_STYLE`.
- Folders Unassigned, Mine, Assigned, Pending, Closed, Spam, All, Starred
  and Trash with counts; search, tag filter and paging (50 per page, at
  most 100) in the conversation list.
- Replies, notes, forwards and new conversations with a rich text editor
  (BlockNote), sent as HTML with a plain-text alternative; reply-all with
  editable CC (at most 20 addresses); a 10-second undo; a Retry button for
  replies that could not be sent; collision warnings ("Alice is replying").
- Tags (at most 20 per conversation), stars, merge two conversations, move
  a conversation to another mailbox, start a conversation with a customer.
- Customer profiles: name, notes, several addresses, past conversations,
  merge two customers, and customer search.
- Per-mailbox signature, auto-reply (never to automatic, list or `noreply`
  mail, at most once per customer and mailbox in 24 hours) and saved
  replies (per mailbox or global).
- Email notifications for new conversations and conversations assigned to
  you, off by default, each person turns them on under Settings. Needs the
  new `NOTIFY_FROM` and either `SMTP_HOST` or the Google shared mailbox.
- **Trash.** Deleting an email or conversation moves it to Desk's trash and
  the message to Gmail's trash; both can be restored. Deleting a mailbox
  moves it to Desk's mailbox trash and suspends it in Google; it is purged
  automatically after 30 days (configurable), and Desk admins get email
  reminders 7 days and 1 day before.
- **Retention**: periods for closed conversations, attachments, raw mail,
  the trash and the mailbox trash, from Den's module settings or Desk's own
  Settings page. Until someone sets them, Desk keeps everything and the
  Settings page shows "pending legal review".
- **Audit log** (`GET /api/v1/audit`, Desk admins) of mailbox changes,
  deletes, restores, purges and retention runs.
- Keyboard shortcuts in the web app (`?` lists them).
- `DESK_ALLOWED_MAIL_HOSTS` (optional): host names or CIDRs of mail servers
  on private addresses that Desk may connect to.
- `server reencrypt` command and `DESK_SECRET_KEY_PREVIOUS` to rotate
  `DESK_SECRET_KEY`; see
  [Operations](https://aglelectronics.github.io/con-services/modules/desk/operations/#rotate-desk_secret_key).
- Architecture decisions in `docs/adr/` (0001–0009).
- `DEN_MODE=required` keeps the module locked until it is paired with Den:
  the API answers `503 den_pairing_required` (except health, brand, sign-in
  and the Den connection routes) and the public `GET /api/den/state`
  (also at `/api/v1/den/state`) reports `{"mode","paired"}`. The default
  `standalone` runs without Den; any other value stops the backend at
  startup.
- The web app shows a "Connect this module to Den" page (admins) or
  "Waiting for Den" (everyone else) while the backend runs with
  `DEN_MODE=required` and is not paired yet.
- `TRUSTED_PROXY_CIDRS` (optional): comma-separated CIDRs of the proxies in
  front of the backend whose `X-Forwarded-For` entries are believed. The
  default, `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128,fc00::/7`,
  covers the Gateway, nginx and Caddy; change it only for a proxy outside
  the private ranges, or set `none` when no proxy is in front. See
  [Client IP addresses](https://aglelectronics.github.io/con-services/operations/production/#client-ip-addresses).
- The backend can serve HTTPS. Set `TLS_CERT_FILE` and `TLS_KEY_FILE` to a
  PEM certificate and private key; the backend re-reads both every minute,
  so a certificate renewed by cert-manager is used without a restart. With
  neither set it serves plain HTTP, which is what Docker Compose
  development uses. See
  [Internal TLS](https://aglelectronics.github.io/con-services/operations/internal-tls/).
- The web app reads its installation settings (sign-in mode, issuer, client
  id, scopes, API address and product name) from `/config.json` when it
  starts, so the published `desk-frontend` image works for every
  installation. nginx serves the file from `/etc/conservices/config.json`
  with `Cache-Control: no-store`, so a change shows on the next page load.
  Without a mounted file the web app shows "Not configured yet"; a broken
  file gives a clear error page. See
  [Web app settings](https://aglelectronics.github.io/con-services/modules/desk/configuration/#web-app-settings-configjson).
- Kubernetes: Helm chart `deploy/charts/desk` `1.0.0` in `con-services`,
  released with the tag `desk/v1.0.0`. Internal TLS is on by default:
  cert-manager issues `desk-backend-tls` and `desk-frontend-tls`, the
  backend and the web app serve HTTPS inside the cluster (web app Service
  on port 443, backend on 8080, both named `https`), and Den's address
  (`DEN_INTERNAL_URL`) uses `https://`. The cluster needs cert-manager,
  trust-manager and the platform's `conservices-internal-ca` issuer;
  without them set `internalTLS.enabled: false`. See
  [Set up Desk](https://aglelectronics.github.io/con-services/modules/desk/setup/).
- Development: `infra/docker-compose.yml` mounts
  `infra/frontend-config/compose.json` (dev sign-in) as the web app's
  `/config.json`; set `FRONTEND_CONFIG` in `.env` to use another file.
  Building from source needs Go 1.27.4 or newer.
- CI on every pull request (tests, lint, Trivy scans) and signed images with
  an SBOM published to GHCR on version tags (linux/amd64; linux/arm64
  optional).

### Changed

- `OIDC_GROUPS_CLAIM`, `OIDC_GROUP_MATCH`, `OIDC_GROUP_PREFIX` and `DEN_ALLOWED_HOSTS` are in the configuration reference and `.env.example`.
- Den's backend is admitted by `networkPolicy.den` (on by default). `allowFromNamespaces` no longer lists `conservices-den`.
- CI passes `den-major: "1"`. A release whose MAJOR is not 1 is refused once the shared `ci-v1` tag points at the workflow that has that input.
- Standalone Compose uses PostgreSQL `postgres:16.15-alpine`.
- The Helm chart description says CI renders and lints the chart; the first production install is still pending.
- **BREAKING:** `AUTH_MODE` defaults to `oidc` and `APP_ENV` defaults to `production`. Set `APP_ENV=development` explicitly for local Compose and tests. `AUTH_MODE=dev` is refused outside development, and the `X-Dev-User` / `X-Dev-Groups` headers are removed and left out of CORS in every other mode.
- **BREAKING:** the API accepts only an access token whose audience is `desk-api` (`OIDC_AUDIENCE`, that default). An ID token is refused. The web app keeps the access token in memory and signs in silently after a reload; it no longer sends the ID token. Point Keycloak's audience mapper at `desk-api` (the dev realm already does) and set `OIDC_AUDIENCE=desk-api`.
- **BREAKING:** Google settings were renamed to the shared names. Use `GOOGLE_WORKSPACE_CREDENTIALS_FILE` and `GOOGLE_WORKSPACE_ADMIN_EMAIL`. The old names `GOOGLE_SERVICE_ACCOUNT_FILE` and `GOOGLE_ADMIN_USER` stop the backend from starting, with a message that names the new variable.
- **BREAKING:** `GET /api/role-catalog` is no longer public. Call it with `HR_SERVICE_TOKEN` (at least 32 characters, the same value HR has) or with a staff access token. The catalog now sends `revision` (also the `ETag`; `If-None-Match` answers 304), `group_display`, `privileged` and `dynamic`. Delete roles and Desk admin are privileged; the three roles of each mailbox are dynamic.
- **Action required:** in the Google Admin console, open **Security → Access and data control → API controls → Domain-wide delegation**, edit the Desk service account's client ID, and add this scope (leave the scopes already there):

  `https://www.googleapis.com/auth/gmail.settings.sharing`

  Desk uses it only to add and remove the send-as address of a group mailbox on the shared account. `gmail.modify` does not cover that. Without this scope those calls are rejected and a group mailbox cannot be created or suspended. Changes can take a few minutes.
- Gmail labels, restoring mail from trash, and send-as aliases now go through the shared Google client. A send-as address that already exists, and one that is already gone, still count as success.
- **BREAKING (pre-release builds only):** the roles `desk-agent`
  (`agent`, permission `tickets.work`) are gone. Add people to the groups
  `desk-<slug>-viewer`, `desk-<slug>-manager` or `desk-<slug>-delete` of
  each mailbox instead (HR's access picker lists them), and remove the
  `desk-agent` group from Keycloak.
- **BREAKING (pre-release builds only):** S3 is now required; attachments
  are no longer stored in PostgreSQL. Set the `S3_*` settings and create the
  bucket before upgrading a development database.
- The backend refuses to start on invalid settings (`FETCH_INTERVAL`
  under 5 seconds, a short `DESK_SECRET_KEY` outside development, a malformed `NOTIFY_FROM`, a Google
  domain without a service account) instead of falling back to defaults.
- On shutdown the fetcher and sender finish their current step before the
  database closes; a reply being sent is queued again, never sent twice.
- Helm chart `deploy/charts/desk`: S3 settings with the bucket `desk-data`
  and the platform data egress to S3; optional Google Workspace settings,
  the service account key mounted from a Secret, and egress to Google's
  APIs; `NOTIFY_FROM` and `DESK_ALLOWED_MAIL_HOSTS`.

### Security

- Mailbox passwords are encrypted at rest with `DESK_SECRET_KEY` and never
  returned by the API. Each value carries a key id and is bound to its
  mailbox and column, so it cannot be copied to another mailbox. Outside
  development the key must be at least 32 characters and not the built-in
  development key.
- Plaintext IMAP and SMTP (`security: none`, `SMTP_SECURITY=none`) are
  refused outside `APP_ENV=development`.
- Desk refuses to connect to private, loopback and link-local addresses
  (checked at connect time) unless they are listed in
  `DESK_ALLOWED_MAIL_HOSTS`; connection errors shown in the web app are
  generic, details are only in the log.
- Changing a mailbox's IMAP or SMTP host or username needs the password
  again, so a stored password is never sent to a new host.
- A failing mailbox is polled less often (doubling, at most hourly), which
  avoids account lockouts.
- Incoming HTML is sanitised and shown in a sandboxed frame with remote
  images blocked until "Show images". Attachments are served with
  `Content-Security-Policy: sandbox` and `X-Content-Type-Options: nosniff`;
  only images, PDF and plain text are shown inline, everything else is a
  download.
- Mailboxes and conversations you have no role for answer 404; mailbox
  connection details (hosts, usernames, errors) are only shown to Desk
  admins.
- The backend reads the client address from `X-Forwarded-For` only when
  the connection comes from a trusted proxy (`TRUSTED_PROXY_CIDRS`), and
  ignores `X-Real-IP` and `True-Client-IP`.
- On Kubernetes the database URL Den shows ends in
  `?sslmode=verify-full&sslrootcert=/etc/conservices-internal-ca/ca.crt`;
  the platform's PostgreSQL refuses connections without TLS.
- No image runs as root. The web app runs on
  `nginxinc/nginx-unprivileged:1.30.6-alpine` as UID 101 and listens on
  port 8080. All images are built with Go 1.27.4 and pass the CI image
  scan (Trivy, HIGH and CRITICAL issues that have a fix).
- A production build never falls back to dev sign-in: a `/config.json`
  without `authMode` means OIDC.
- The S3 client now uses `github.com/klauspost/compress` 1.20.2.
  1.18.5 had CVE-2026-63209 (denial of service in dictionary processing,
  fixed in 1.18.7) and CVE-2026-116591 (a crafted zstd frame panics the
  decoder, fixed in 1.20.2). Image and repository scans that gate on HIGH
  and CRITICAL findings were failing on this module.
- The backend and the migrate tool are built with Go 1.27.4. Go 1.27.3's
  `net/http` can panic when `MaxBytesReader` hits its limit while response
  headers are written (CVE-2026-97054). Desk uses `MaxBytesReader` to limit
  JSON bodies.
