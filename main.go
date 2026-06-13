package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/billysword/sword-flowers/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuin/goldmark"
)

type templates struct {
	list      *template.Template
	detail    *template.Template
	newPost   *template.Template
	adminList *template.Template
	editPost  *template.Template
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
	adminList, err := template.ParseFiles("templates/base.html", "templates/admin/posts.html")
	if err != nil {
		return nil, err
	}
	editPost, err := template.ParseFiles("templates/base.html", "templates/admin/edit_post.html")
	if err != nil {
		return nil, err
	}
	return &templates{list: list, detail: detail, newPost: newPost, adminList: adminList, editPost: editPost}, nil
}

func main() {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	bucketName := os.Getenv("BUCKET_NAME")
	if bucketName == "" {
		log.Fatal("BUCKET_NAME is required")
	}

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	log.Println("database connected")

	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("create GCS client: %v", err)
	}
	defer gcsClient.Close()
	log.Println("GCS client ready")

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
	http.HandleFunc("/admin/posts", adminPostsHandler(pool, tmpl, gcsClient, bucketName))
	http.HandleFunc("/admin/posts/new", newPostFormHandler(tmpl))
	http.HandleFunc("/admin/posts/{slug}/edit", editPostFormHandler(pool, tmpl))
	http.HandleFunc("/admin/posts/{slug}/delete", deletePostHandler(pool))
	http.HandleFunc("/admin/posts/{slug}", updatePostHandler(pool, gcsClient, bucketName))

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
		var imageRef *string
		err := pool.QueryRow(r.Context(),
			`SELECT subject, body, image_ref FROM posts WHERE slug = $1 AND status = 'published'`,
			slug).Scan(&subject, &body, &imageRef)
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
			Subject  string
			Body     template.HTML
			ImageRef string
		}{
			Subject:  subject,
			Body:     rendered,
			ImageRef: strVal(imageRef),
		}

		if err := tmpl.detail.ExecuteTemplate(w, "base", data); err != nil {
			log.Printf("get post render: %v", err)
		}
	}
}

func adminPostsHandler(pool *pgxpool.Pool, tmpl *templates, gcsClient *storage.Client, bucketName string) http.HandlerFunc {
	getHandler := adminListHandler(pool, tmpl)
	postHandler := createPostHandler(pool, gcsClient, bucketName)
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getHandler(w, r)
		case http.MethodPost:
			postHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func adminListHandler(pool *pgxpool.Pool, tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(),
			`SELECT slug, subject, status FROM posts ORDER BY created_at DESC`)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			log.Printf("admin list posts: %v", err)
			return
		}
		defer rows.Close()

		type adminPost struct {
			Slug    string
			Subject string
			Status  string
		}
		var posts []adminPost
		for rows.Next() {
			var p adminPost
			if err := rows.Scan(&p.Slug, &p.Subject, &p.Status); err != nil {
				http.Error(w, "scan failed", http.StatusInternalServerError)
				log.Printf("admin list scan: %v", err)
				return
			}
			posts = append(posts, p)
		}

		data := struct{ Posts []adminPost }{Posts: posts}
		if err := tmpl.adminList.ExecuteTemplate(w, "base", data); err != nil {
			log.Printf("admin list render: %v", err)
		}
	}
}

func editPostFormHandler(pool *pgxpool.Pool, tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		var subject, body, status string
		var imageRef *string
		err := pool.QueryRow(r.Context(),
			`SELECT slug, subject, body, status, image_ref FROM posts WHERE slug = $1`,
			slug).Scan(&slug, &subject, &body, &status, &imageRef)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		data := struct {
			Slug     string
			Subject  string
			Body     string
			Status   string
			ImageRef string
		}{slug, subject, body, status, strVal(imageRef)}

		if err := tmpl.editPost.ExecuteTemplate(w, "base", data); err != nil {
			log.Printf("edit post form render: %v", err)
		}
	}
}

func updatePostHandler(pool *pgxpool.Pool, gcsClient *storage.Client, bucketName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		slug := r.PathValue("slug")

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "could not parse form", http.StatusBadRequest)
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

		var newImageRef string
		file, header, err := r.FormFile("image")
		if err == nil {
			defer file.Close()
			ext := filepath.Ext(header.Filename)
			newImageRef, err = uploadImage(r.Context(), gcsClient, bucketName, slug, ext, file)
			if err != nil {
				http.Error(w, "image upload failed", http.StatusInternalServerError)
				log.Printf("update post upload image: %v", err)
				return
			}
		}

		var q string
		var args []any
		if newImageRef != "" {
			q = `UPDATE posts SET subject=$1, body=$2, status=$3, image_ref=$4, updated_at=now() WHERE slug=$5`
			args = []any{subject, body, status, newImageRef, slug}
		} else {
			q = `UPDATE posts SET subject=$1, body=$2, status=$3, updated_at=now() WHERE slug=$4`
			args = []any{subject, body, status, slug}
		}

		if _, err := pool.Exec(r.Context(), q, args...); err != nil {
			http.Error(w, "could not update post", http.StatusInternalServerError)
			log.Printf("update post: %v", err)
			return
		}

		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
	}
}

func deletePostHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		slug := r.PathValue("slug")
		if _, err := pool.Exec(r.Context(), `DELETE FROM posts WHERE slug = $1`, slug); err != nil {
			http.Error(w, "could not delete post", http.StatusInternalServerError)
			log.Printf("delete post: %v", err)
			return
		}

		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
	}
}

func newPostFormHandler(tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := tmpl.newPost.ExecuteTemplate(w, "base", nil); err != nil {
			log.Printf("new post form render: %v", err)
		}
	}
}

func createPostHandler(pool *pgxpool.Pool, gcsClient *storage.Client, bucketName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "could not parse form", http.StatusBadRequest)
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

		var imageRef string
		file, header, err := r.FormFile("image")
		if err == nil {
			defer file.Close()
			slug := slugify(subject)
			ext := filepath.Ext(header.Filename)
			imageRef, err = uploadImage(r.Context(), gcsClient, bucketName, slug, ext, file)
			if err != nil {
				http.Error(w, "image upload failed", http.StatusInternalServerError)
				log.Printf("upload image: %v", err)
				return
			}
		}

		slug, err := insertPost(r.Context(), pool, subject, body, status, imageRef)
		if err != nil {
			http.Error(w, "could not save post", http.StatusInternalServerError)
			log.Printf("create post: %v", err)
			return
		}

		http.Redirect(w, r, "/posts/"+slug, http.StatusSeeOther)
	}
}

func uploadImage(ctx context.Context, client *storage.Client, bucket, slug, ext string, r io.Reader) (string, error) {
	object := fmt.Sprintf("posts/%s-%d%s", slug, time.Now().UnixMilli(), ext)
	wc := client.Bucket(bucket).Object(object).NewWriter(ctx)
	if _, err := io.Copy(wc, r); err != nil {
		wc.Close()
		return "", fmt.Errorf("write to GCS: %w", err)
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("close GCS writer: %w", err)
	}
	return "https://storage.googleapis.com/" + bucket + "/" + object, nil
}

// insertPost inserts a new post and returns its slug. On unique slug collision
// it retries with a numeric suffix (-2, -3, ...).
func insertPost(ctx context.Context, pool *pgxpool.Pool, subject, body, status, imageRef string) (string, error) {
	base := slugify(subject)
	slug := base
	for i := 2; i <= 100; i++ {
		var ref *string
		if imageRef != "" {
			ref = &imageRef
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO posts (slug, subject, body, status, image_ref) VALUES ($1, $2, $3, $4, $5)`,
			slug, subject, body, status, ref)
		if err == nil {
			return slug, nil
		}
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

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
