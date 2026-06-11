package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

	"github.com/billysword/sword-flowers/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuin/goldmark"
)

type templates struct {
	list   *template.Template
	detail *template.Template
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
	return &templates{list: list, detail: detail}, nil
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

func renderMarkdown(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
