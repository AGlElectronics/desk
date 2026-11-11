# Changelog

All notable changes to Desk are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

Needs Den: 1.0.0 or later (Den MAJOR 1).

First release, `1.0.0`. Released versions are tagged `vMAJOR.MINOR.PATCH`;
the images `ghcr.io/aglelectronics/desk-backend`, `desk-frontend` and
`desk-migrate` are published as `MAJOR.MINOR.PATCH-BUILD`, where `BUILD`
counts rebuilds of the same version. Rebuilds get no entry here.

### Added

- Shared-inbox helpdesk: each support address is a mailbox with its own
  IMAP account and optionally its own SMTP server (otherwise a module-wide
  fallback), with a connection test that changes nothing. Customers never
  sign in; to them it is plain email.
- Every email thread becomes a conversation that agents assign, answer,
  annotate and close in the browser, with one assignee per conversation, a
  10-second undo, collision warnings and folder counts per person.
- Roles `desk-agent` and `desk-admin`, published in the role catalog so HR
  can grant them during onboarding.
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
  Building from source needs Go 1.27.3 or newer.
- CI on every pull request (tests, lint, Trivy scans) and signed images with
  an SBOM published to GHCR on version tags (linux/amd64; linux/arm64
  optional).

### Security

- Mailbox passwords are encrypted at rest with `DESK_SECRET_KEY` and never
  returned by the API.
- The backend reads the client address from `X-Forwarded-For` only when
  the connection comes from a trusted proxy (`TRUSTED_PROXY_CIDRS`), and
  ignores `X-Real-IP` and `True-Client-IP`.
- On Kubernetes the database URL Den shows ends in
  `?sslmode=verify-full&sslrootcert=/etc/conservices-internal-ca/ca.crt`;
  the platform's PostgreSQL refuses connections without TLS.
- No image runs as root. The web app runs on
  `nginxinc/nginx-unprivileged:1.30.6-alpine` as UID 101 and listens on
  port 8080. All images are built with Go 1.27.3 and pass the CI image
  scan (Trivy, HIGH and CRITICAL issues that have a fix).
- A production build never falls back to dev sign-in: a `/config.json`
  without `authMode` means OIDC.
