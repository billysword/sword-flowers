# Technical Debt

Cleanup work that is consciously deferred. See CLAUDE.md for the rules.

---

## `main.go` — all post handlers in one file

`main.go` contains routing, handler functions for posts, image upload, gallery
management, and utility helpers (slugify, renderMarkdown) in a single ~620-line
file. Post-related handlers (`listPostsHandler`, `getPostHandler`,
`adminListHandler`, `editPostFormHandler`, `updatePostHandler`,
`deletePostHandler`, `createPostHandler`, `uploadImage`, `uploadGalleryImages`,
`updateGalleryImages`) would naturally live in a `posts.go` file.

**Deferred because:** The file works, everything compiles, and splitting during
active feature work adds churn. Do it when `main.go` next needs a significant
edit.

---

## `chat.go` — session management mixed with API calling

`chat.go` holds both the in-memory session store (`chatStore`, `sessionData`,
`getOrCreateSession`) and the Claude API streaming logic (`chatHandler`). As the
chat surface grows these are separate responsibilities.

**Deferred because:** The file is small enough (~160 lines) that the coupling
isn't painful yet. Revisit if session management needs to become persistent
(DB-backed) or if the chat handler grows significantly.

---

## Chat sessions are in-memory only

`chatStore` is a `sync.Map` in the Go process. All conversation history is lost
on server restart. Cloud Run scales to zero, so idle users lose context on
wakeup.

**Deferred because:** Acceptable for the current exploratory phase. When sessions
need to survive restarts, the fix is to persist `sessionData.history` to Postgres
(or Redis) keyed by the UUID cookie.

---

## Widget HTML is an inline Go string

`widget_stock.go` builds the HTML fragment as a `fmt.Sprintf` string. When widgets
grow complex (conditional sections, loops), move to `templates/widgets/` with
`html/template` files so each widget can own its own markup cleanly.

**Deferred because:** A single-line div doesn't need a template file yet.

---

## No error pages

All errors are returned via `http.Error(w, "...", statusCode)`, which renders as
plain text. The terminal chat page (`/`) is dark; a plain-text error response
will look visually broken.

**Deferred because:** Not user-facing enough to matter yet. Address when the
public surface is more polished.
