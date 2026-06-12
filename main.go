package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/billysword/sword-flowers/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuin/goldmark"
)

type templates struct {
	list    *template.Template
	detail  *template.Template
	newPost *template.Template
}

func loadTemplates() (*templates, error) {
	list, err := template.ParseFiles("templates/base.html", "templates/posts.html")
	if err != nil {
		return nil, err
	}
	detail, err := template.ParseFiles("templates/base.html", "templates/post.html")
	if err != nil {
		return nil, err
	}
	newPost, err := template.ParseFiles("templates/base.html", "templates/admin/new_post.html")
	if err != nil {
		return nil, err
	}
	return &templates{list: list, detail: detail, newPost: newPost}, nil
}

func main() {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	log.Println("database connected")

	tmpl, err := loadTemplates()
	if err != nil {
		log.Fatalf("load templates: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "sword flowers")
	})
	http.HandleFunc("/posts", listPostsHandler(pool, tmpl))
	http.HandleFunc("/posts/{slug}", getPostHandler(pool, tmpl))
	http.HandleFunc("/admin/posts/new", newPostFormHandler(tmpl))
	http.HandleFunc("/admin/posts", createPostHandler(pool))

	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

type postSummary struct {
	Slug    string
	Subject string
}

func listPostsHandler(pool *pgxpool.Pool, tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(),
			`SELECT slug, subject FROM posts WHERE status = 'published' ORDER BY created_at DESC`)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			log.Printf("list posts: %v", err)
			return
		}
		defer rows.Close()

		var posts []postSummary
		for rows.Next() {
			var p postSummary
			if err := rows.Scan(&p.Slug, &p.Subject); err != nil {
				http.Error(w, "scan failed", http.StatusInternalServerError)
				log.Printf("list posts scan: %v", err)
				return
			}
			posts = append(posts, p)
		}

		data := struct{ Posts []postSummary }{Posts: posts}
		if err := tmpl.list.ExecuteTemplate(w, "base", data); err != nil {
			log.Printf("list posts render: %v", err)
		}
	}
}

func getPostHandler(pool *pgxpool.Pool, tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		var subject, body string
		err := pool.QueryRow(r.Context(),
			`SELECT subject, body FROM posts WHERE slug = $1 AND status = 'published'`,
			slug).Scan(&subject, &body)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		rendered, err := renderMarkdown(body)
		if err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
			log.Printf("render markdown: %v", err)
			return
		}

		data := struct {
			Subject string
			Body    template.HTML
		}{Subject: subject, Body: rendered}

		if err := tmpl.detail.ExecuteTemplate(w, "base", data); err != nil {
			log.Printf("get post render: %v", err)
		}
	}
}

func newPostFormHandler(tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := tmpl.newPost.ExecuteTemplate(w, "base", nil); err != nil {
			log.Printf("new post form render: %v", err)
		}
	}
}

func createPostHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		subject := strings.TrimSpace(r.FormValue("subject"))
		body := strings.TrimSpace(r.FormValue("body"))
		status := r.FormValue("status")

		if subject == "" || body == "" {
			http.Error(w, "subject and body are required", http.StatusBadRequest)
			return
		}
		if status != "draft" && status != "published" {
			status = "draft"
		}

		slug, err := insertPost(r.Context(), pool, subject, body, status)
		if err != nil {
			http.Error(w, "could not save post", http.StatusInternalServerError)
			log.Printf("create post: %v", err)
			return
		}

		http.Redirect(w, r, "/posts/"+slug, http.StatusSeeOther)
	}
}

// insertPost inserts a new post and returns its slug. On unique slug collision
// it retries with a numeric suffix (-2, -3, ...).
func insertPost(ctx context.Context, pool *pgxpool.Pool, subject, body, status string) (string, error) {
	base := slugify(subject)
	slug := base
	for i := 2; i <= 100; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO posts (slug, subject, body, status) VALUES ($1, $2, $3, $4)`,
			slug, subject, body, status)
		if err == nil {
			return slug, nil
		}
		// retry only on unique constraint violation
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			slug = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("could not generate unique slug for %q", subject)
}

var (
	nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)
	leadingTrailing = regexp.MustCompile(`^-|-$`)
)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = leadingTrailing.ReplaceAllString(s, "")
	return s
}

func renderMarkdown(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
