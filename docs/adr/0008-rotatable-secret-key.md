# ADR 0008 — DESK_SECRET_KEY is rotatable

Status: accepted (pre-1.0.0, maintainer decision). Changing the stored
format after a release would be a breaking change, so it is done now.

## Context

IMAP and SMTP passwords are stored encrypted with a key derived from
`DESK_SECRET_KEY`. In the MVP there was one key with no version: changing
the key made every stored password unreadable, and the docs said "never
rotate". Ciphertexts were not bound to their row, so one could be copied to
another mailbox.

## Decision

- A sealed value is `v1.<key id>.<base64(nonce|ciphertext)>`, AES-256-GCM.
  The key id is derived from the key and is safe to log (the backend logs
  it at start as `key_id`).
- Every value is bound to its place with GCM additional data
  `mailbox:<id>:<column>`; a ciphertext copied elsewhere does not open.
- `DESK_SECRET_KEY_PREVIOUS` (comma-separated) lists old keys that still
  open values. Values from before key ids (plain base64) still open too.
- `server reencrypt` (the backend binary with the argument `reencrypt`)
  seals every stored password again under the current key in one
  transaction and reports values no key opens.
- Rotation: put the old key in `DESK_SECRET_KEY_PREVIOUS`, set a new
  `DESK_SECRET_KEY`, restart, run `server reencrypt`, then remove the
  previous key and restart again.
- Outside `APP_ENV=development` the key must be at least 32 characters and
  must not be the built-in development key; the built-in key is used only
  when `APP_ENV=development` is set explicitly.

## Consequences

- The key can be rotated without re-entering passwords.
- Losing every key that sealed a password still means re-entering it; the
  rest of Desk's data is not encrypted with this key.
- Google Workspace mailboxes store no passwords; the service account key is
  a separate secret.
