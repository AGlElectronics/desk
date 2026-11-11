# ADR 0007 — BlockNote rich text for replies, sent as HTML plus plain text

Status: accepted (pre-1.0.0, maintainer decision).

## Context

The MVP had a plain textarea. Support answers need links, lists and bold
text, and the wiki already uses BlockNote, so the suite has one editor to
learn and maintain.

## Decision

- Replies, notes, forwards, new conversations, saved replies and the
  auto-reply use **BlockNote** (`@blocknote/react` with the Mantine theme),
  loaded lazily so the inbox stays fast.
- The editor's HTML is sent as `body_html`. The backend sanitises it
  (bluemonday allow-list) and derives the **plain-text alternative** when
  the client sends none. Outgoing mail is `multipart/alternative`
  (text and HTML), inside `multipart/mixed` when there are attachments.
- The API keeps accepting plain `body` alone (scripts, older clients);
  Desk then builds the HTML from the text.
- No live co-editing: one person writes a reply.

## Consequences

- The frontend bundle grows (BlockNote and Mantine); the lazy chunk keeps
  it off the first page load.
- What the customer sees is the sanitised HTML; anything the allow-list
  removes (scripts, forms, styles from outside) never leaves Desk.
