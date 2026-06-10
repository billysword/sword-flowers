# ADR 002: Post body format and status field

## Decision

- `body` is stored as **Markdown** and rendered to HTML server-side before templating.
- Posts have a `status` field (`draft` | `published`) in v1.

## Rationale

Posts are expected to be a few paragraphs; Markdown gives authors lightweight
formatting without a WYSIWYG editor or stored HTML. A Go Markdown parser (e.g.
goldmark) is a small, self-contained dependency.

Status exists so posts can be saved without being immediately public. Simpler than
building a separate draft store.

## Consequences

A Markdown parser dependency will be added at CP2/CP3 when rendering is wired up.
If plain text proves sufficient, a migration dropping Markdown rendering is trivial.
