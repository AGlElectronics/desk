# ADR 0004 — Mail host safety: TLS only, no internal addresses, password on host change

Status: accepted (pre-1.0.0, maintainer decision).

## Context

Desk admins type IMAP and SMTP hosts into a form, and Desk connects to
them from inside the cluster. Without limits that is a port scanner
("Test connection" against `10.0.0.12:5432`), a way to send stored
passwords to a host of the attacker's choosing, and a plaintext login
over the network.

## Decision

- **No plaintext outside development.** `security=none` for a mailbox, and
  `SMTP_SECURITY=none` for the module-wide SMTP server, are accepted only
  with `APP_ENV=development` (GreenMail and Mailpit). Elsewhere they are
  refused when saved and when used.
- **No internal addresses by default.** Every resolved address is checked
  at connect time (so DNS rebinding is caught too). Private, loopback,
  link-local, CGNAT, multicast and unspecified ranges are refused unless
  the host name or a CIDR is listed in `DESK_ALLOWED_MAIL_HOSTS`, for
  on-site mail servers.
- **Generic errors.** The connection test and `last_fetch_error` show a
  short message ("could not connect", "login failed"); the details are only
  in the backend log.
- **Password with every host change.** Changing an IMAP or SMTP host or
  username needs that password in the same request; Desk never sends a
  stored password to a host it was not entered for.
- **Back-off.** A mailbox that keeps failing is polled less often (the
  interval doubles per failure, at most one hour), which also protects
  against account lockouts.

## Consequences

- An on-site mail server on a private address needs one line in
  `DESK_ALLOWED_MAIL_HOSTS`; the setup docs say so.
- The chart's NetworkPolicy is the second line of defence; it filters by
  port, Desk filters by address.
