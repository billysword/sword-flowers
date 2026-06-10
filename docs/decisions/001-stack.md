# ADR 001: Stack

## Decision

- **Backend:** Go, `net/http` stdlib for routing
- **Frontend:** HTMX + `html/template`, no build step, `htmx.min.js` vendored
- **Database:** Neon (serverless Postgres), `pgx`, hand-written SQL
- **Images:** Google Cloud Storage, object key stored in Postgres
- **Hosting:** Cloud Run (scales to zero)

## Rationale

Deliberate simplicity. No ORM, no bundler, no framework magic — each layer is
legible on its own. Cloud Run + Neon both scale to zero, so idle cost is near zero.
HTMX keeps the server as the single source of truth; no client-side state store.

## Consequences

Adding a dependency requires explicit approval and an ADR update.
