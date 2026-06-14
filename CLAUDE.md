# CLAUDE.md

Operating instructions for Claude on this project. Read this fully before doing anything.

## What we're building

A small content CMS and the public website that renders its content. One Postgres
database, two surfaces: an authenticated admin (create/edit/delete posts) and an
anonymous, read-only public site. **Build the CMS first, then the site.**

Scope is deliberately small. A "post" is roughly a photo, a subject, and a body.
That's it — see the Data Model section and do not expand it.

This project is also a learning vehicle: the humans are deliberately learning the
web platform, CSS, HTMX, Go, and Cloud Run. **Optimize for their understanding, not
for speed.**

## Stack (locked)

- **Backend:** Go. Standard library first.
- **Frontend:** HTMX over server-rendered HTML, using `html/template`. `htmx.min.js`
  is vendored into the repo — no npm, no bundler, no front-end build step.
- **Database:** Neon (serverless Postgres), accessed via `pgx` with hand-written SQL.
  No ORM (no GORM, ent, etc.) and no `sqlc` unless explicitly added later.
- **Migrations:** plain versioned `.sql` files. Runner chosen in CP1.
- **Images:** Google Cloud Storage bucket. Store the object key/URL in Postgres —
  never image bytes in the database.
- **Hosting:** Cloud Run (container, scales to zero). Secrets via env vars / Secret Manager.

Do not introduce other frameworks, libraries, or services without explicit approval.

## How to work here (the important part)

- **Build only what was asked.** No speculative features, abstractions, config,
  endpoints, or dependencies. No "while I'm here" additions. If you think something
  extra is warranted, *propose it and wait* — don't build it.
- **When in doubt, stop and ask.** If a requirement is ambiguous or underspecified,
  ask a question rather than filling the gap with an assumption.
- **Plan before code.** For anything non-trivial, post a short plan (files to touch,
  approach) and wait for approval before writing it.
- **Small, reviewable steps.** One logical change per commit. Keep every diff small
  enough for a human to read and fully understand in one sitting. We do not merge
  code we haven't read.
- **Pace for understanding, not velocity.** Don't run ahead. After each checkpoint,
  stop for human review before starting the next.
- **Simplest thing that works.** Standard library before dependencies. No premature
  abstraction. Comment the *why*, never restate the code.

## Teaching calibration

The humans are senior engineers (15+ years, mobile / Kotlin / Compose + backend).

- **Assume** deep knowledge of: data modeling, concurrency, API design, testing,
  git, general architecture. Do not explain these.
- **Do explain** (briefly, when first introduced): web-platform fundamentals, CSS
  (cascade, specificity, flexbox/grid), HTMX patterns, Go idioms that differ from
  Kotlin, and Cloud Run / Neon / GCS specifics.
- Flag the mental shift from Compose when relevant: the state of record lives on the
  **server / in the DB**, not in client-held reactive state.

## Conventions

**Go**
- Start with stdlib `net/http` for routing; add a router (e.g. chi) only if/when
  justified and approved.
- Explicit error handling — return errors, handle them at the boundary.
- `html/template` for all rendering. HTMX endpoints return HTML fragments, not JSON.

**HTMX**
- The server is the single source of truth. No client-side state store.
- Interactions are declared with `hx-*` attributes and return HTML fragments swapped
  into the DOM.
- Plain HTML forms plus progressive enhancement where reasonable.

**Database**
- Connection string from `DATABASE_URL`.
- Hand-written SQL via `pgx`. No query builders, no ORM.
- Migrations are versioned `.sql` files committed to the repo.

## Data model (the contract)

Starting shape — treat as fixed. **Do not add fields without explicit approval.**

```
posts
  id
  slug
  subject
  body
  image_ref    -- GCS object key/URL
  status       -- decide in CP1 whether this exists at all
  created_at
  updated_at
```

Open questions to settle in CP1 (ask, don't assume):
- Is `body` plain text, markdown, or rich text?
- Do we want draft/published `status` in v1, or is every post live?

## Checkpoints

Each checkpoint compiles, runs, is reviewed by a human, and is committed before the
next begins.

- **CP0** — Repo conventions, this file, `/docs/decisions/`, a minimal Dockerfile,
  and a hello-world handler deployed to Cloud Run. Goal: a live HTTPS URL with zero
  app logic. Prove the pipeline first.
- **CP1** — Lock the data model; write the first migration. Settle the open questions above.
- **CP2** — Neon connectivity from Go; seed data; prove one query end to end.
- **CP3** — Read path: list and view posts, server-rendered, minimal CSS.
- **CP4** — Write path: create post (form, no image yet).
- **CP5** — Image upload to GCS.
- **CP6** — Edit / delete.
- **CP7** — Minimal auth gate on the admin surface.

## Decision log

Non-trivial decisions (stack choices, schema, storage model) get a short ADR in
`/docs/decisions/`. Don't re-litigate logged decisions; if one needs revisiting,
propose a new ADR.

## Commands

- **Deploy:** `gcloud run deploy --source .` (or via Dockerfile)
- **Local dev:** `./dev.sh` (loads `.env` then runs `go run .`)
- **Migrate:** `migrate -database "$DATABASE_URL" -path migrations up`
