# ADR 004: Gallery images per post

## Decision

- `posts.image_ref` is unchanged: it remains the single cover/featured image
  used in list views and at the top of a post.
- A new `post_images` table holds additional gallery photos for a post:
  ```sql
  CREATE TABLE post_images (
      id         BIGSERIAL PRIMARY KEY,
      post_id    UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
      image_ref  TEXT NOT NULL,
      caption    TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  ```
- Display order is implied by `(created_at, id)` — no explicit `position`
  column. Captions are optional per image.

## Rationale

The cover image and the gallery serve different purposes: the cover is shown
wherever a post is summarized (list pages), while the gallery is only relevant
on the post detail page. Keeping `image_ref` as-is avoids touching every place
that already reads it.

A normalized table (rather than a JSONB column or array on `posts`) keeps
gallery rows queryable and lets Postgres enforce referential integrity via the
foreign key, consistent with "hand-written SQL, no ORM, but still relational."

No `position` column for v1: nothing today needs manual reordering, and adding
one later is a small, additive migration. Upload order is a reasonable default.

## Consequences

Deleting a post cascades to its gallery rows, but — same as the existing gap
for `image_ref` — the underlying GCS objects are not cleaned up. That's a
pre-existing limitation, not introduced here.
