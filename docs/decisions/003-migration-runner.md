# ADR 003: Migration runner

## Decision

Use `golang-migrate` CLI to run plain versioned `.sql` files.

## Rationale

Standard choice in the Go ecosystem. Handles up/down migrations, tracks applied
versions in a `schema_migrations` table, and requires no changes to application
code — migrations are a separate operational step.

## Consequences

Install the CLI locally and in the deploy pipeline when needed:
`go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
