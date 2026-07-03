package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
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
	chat      *template.Template
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
	// chat.html is a standalone page — no base.html.
	chat, err := template.ParseFiles("templates/chat.html")
	if err != nil {
		return nil, err
	}
	return &templates{list: list, detail: detail, newPost: newPost, adminList: adminList, editPost: editPost, chat: chat}, nil
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

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	if googleClientID == "" {
		log.Fatal("GOOGLE_CLIENT_ID is required")
	}
	googleClientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if googleClientSecret == "" {
		log.Fatal("GOOGLE_CLIENT_SECRET is required")
	}
	allowedEmails := os.Getenv("ALLOWED_EMAILS")
	if allowedEmails == "" {
		log.Fatal("ALLOWED_EMAILS is required")
	}
	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		log.Fatal("SESSION_SECRET is required")
	}
	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		log.Fatal("BASE_URL is required")
	}

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		log.Fatal("ANTHROPIC_API_KEY is required")
	}

	oauthCfg := newOAuthConfig(googleClientID, googleClientSecret, baseURL)

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

	admin := func(h http.HandlerFunc) http.HandlerFunc {
		return requireAdmin(sessionSecret, h)
	}

	// Serve files from the static/ directory at /static/*.
	// StripPrefix removes the /static/ prefix before looking up the file on disk.
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	anthropicClient := newAnthropicClient(anthropicKey)
	http.HandleFunc("/", chatPageHandler(tmpl))
	http.HandleFunc("/chat", chatHandler(anthropicClient))
	http.HandleFunc("/posts", listPostsHandler(pool, tmpl))
	http.HandleFunc("/posts/{slug}", getPostHandler(pool, tmpl))

	http.HandleFunc("/auth/login", loginHandler(oauthCfg))
	http.HandleFunc("/auth/callback", callbackHandler(oauthCfg, sessionSecret, allowedEmails, baseURL))
	http.HandleFunc("/auth/logout", logoutHandler())

	http.HandleFunc("/admin/posts", admin(adminPostsHandler(pool, tmpl, gcsClient, bucketName)))
	http.HandleFunc("/admin/posts/new", admin(newPostFormHandler(tmpl)))
	http.HandleFunc("/admin/posts/{slug}/edit", admin(editPostFormHandler(pool, tmpl)))
	http.HandleFunc("/admin/posts/{slug}/delete", admin(deletePostHandler(pool)))
	http.HandleFunc("/admin/posts/{slug}", admin(updatePostHandler(pool, gcsClient, bucketName)))

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

		var id, subject, body string
		var imageRef *string
		err := pool.QueryRow(r.Context(),
			`SELECT id, subject, body, image_ref FROM posts WHERE slug = $1 AND status = 'published'`,
			slug).Scan(&id, &subject, &body, &imageRef)
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

		gallery, err := getPostImages(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			log.Printf("get post gallery: %v", err)
			return
		}

		data := struct {
			Subject  string
			Body     template.HTML
			ImageRef string
			Gallery  []postImage
		}{
			Subject:  subject,
			Body:     rendered,
			ImageRef: strVal(imageRef),
			Gallery:  gallery,
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

		var id, subject, body, status string
		var imageRef *string
		err := pool.QueryRow(r.Context(),
			`SELECT id, slug, subject, body, status, image_ref FROM posts WHERE slug = $1`,
			slug).Scan(&id, &slug, &subject, &body, &status, &imageRef)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		gallery, err := getPostImages(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			log.Printf("edit post gallery: %v", err)
			return
		}

		data := struct {
			Slug     string
			Subject  string
			Body     string
			Status   string
			ImageRef string
			Gallery  []postImage
		}{slug, subject, body, status, strVal(imageRef), gallery}

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

		if err := r.ParseMultipartForm(30 << 20); err != nil {
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
			q = `UPDATE posts SET subject=$1, body=$2, status=$3, image_ref=$4, updated_at=now() WHERE slug=$5 RETURNING id`
			args = []any{subject, body, status, newImageRef, slug}
		} else {
			q = `UPDATE posts SET subject=$1, body=$2, status=$3, updated_at=now() WHERE slug=$4 RETURNING id`
			args = []any{subject, body, status, slug}
		}

		var postID string
		if err := pool.QueryRow(r.Context(), q, args...).Scan(&postID); err != nil {
			http.Error(w, "could not update post", http.StatusInternalServerError)
			log.Printf("update post: %v", err)
			return
		}

		if err := updateGalleryImages(r.Context(), pool, postID, r.MultipartForm.Value); err != nil {
			http.Error(w, "could not update gallery", http.StatusInternalServerError)
			log.Printf("update post gallery: %v", err)
			return
		}

		if err := uploadGalleryImages(r.Context(), pool, gcsClient, bucketName, slug, postID, r.MultipartForm.File["gallery"]); err != nil {
			http.Error(w, "gallery upload failed", http.StatusInternalServerError)
			log.Printf("update post gallery upload: %v", err)
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

		if err := r.ParseMultipartForm(30 << 20); err != nil {
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

		slug, postID, err := insertPost(r.Context(), pool, subject, body, status, imageRef)
		if err != nil {
			http.Error(w, "could not save post", http.StatusInternalServerError)
			log.Printf("create post: %v", err)
			return
		}

		if err := uploadGalleryImages(r.Context(), pool, gcsClient, bucketName, slug, postID, r.MultipartForm.File["gallery"]); err != nil {
			http.Error(w, "gallery upload failed", http.StatusInternalServerError)
			log.Printf("create post gallery: %v", err)
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

// insertPost inserts a new post and returns its slug and id. On unique slug
// collision it retries with a numeric suffix (-2, -3, ...).
func insertPost(ctx context.Context, pool *pgxpool.Pool, subject, body, status, imageRef string) (slug, id string, err error) {
	base := slugify(subject)
	slug = base
	for i := 2; i <= 100; i++ {
		var ref *string
		if imageRef != "" {
			ref = &imageRef
		}
		err = pool.QueryRow(ctx,
			`INSERT INTO posts (slug, subject, body, status, image_ref) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			slug, subject, body, status, ref).Scan(&id)
		if err == nil {
			return slug, id, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			slug = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		return "", "", err
	}
	return "", "", fmt.Errorf("could not generate unique slug for %q", subject)
}

type postImage struct {
	ID       int64
	ImageRef string
	Caption  string
}

func getPostImages(ctx context.Context, pool *pgxpool.Pool, postID string) ([]postImage, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, image_ref, caption FROM post_images WHERE post_id = $1 ORDER BY created_at, id`,
		postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []postImage
	for rows.Next() {
		var img postImage
		var caption *string
		if err := rows.Scan(&img.ID, &img.ImageRef, &caption); err != nil {
			return nil, err
		}
		img.Caption = strVal(caption)
		images = append(images, img)
	}
	return images, rows.Err()
}

// uploadGalleryImages uploads each file to GCS and adds it to the post's
// gallery. Captions aren't set here; they're added afterward via edit.
func uploadGalleryImages(ctx context.Context, pool *pgxpool.Pool, client *storage.Client, bucket, slug, postID string, files []*multipart.FileHeader) error {
	for i, header := range files {
		f, err := header.Open()
		if err != nil {
			return fmt.Errorf("open gallery file: %w", err)
		}
		ext := filepath.Ext(header.Filename)
		ref, err := uploadImage(ctx, client, bucket, fmt.Sprintf("%s-gallery-%d", slug, i), ext, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("upload gallery image: %w", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO post_images (post_id, image_ref) VALUES ($1, $2)`,
			postID, ref); err != nil {
			return fmt.Errorf("insert gallery image: %w", err)
		}
	}
	return nil
}

// updateGalleryImages applies caption edits and deletions submitted from the
// edit form, where each existing image contributes a "caption_<id>" field and,
// if its checkbox was ticked, a "delete_image_<id>" field.
func updateGalleryImages(ctx context.Context, pool *pgxpool.Pool, postID string, values map[string][]string) error {
	for key := range values {
		imageID, ok := strings.CutPrefix(key, "delete_image_")
		if !ok {
			continue
		}
		if _, err := pool.Exec(ctx,
			`DELETE FROM post_images WHERE id = $1 AND post_id = $2`,
			imageID, postID); err != nil {
			return fmt.Errorf("delete gallery image: %w", err)
		}
	}

	for key, vals := range values {
		imageID, ok := strings.CutPrefix(key, "caption_")
		if !ok {
			continue
		}
		if _, ok := values["delete_image_"+imageID]; ok {
			continue
		}
		caption := strings.TrimSpace(vals[0])
		var c *string
		if caption != "" {
			c = &caption
		}
		if _, err := pool.Exec(ctx,
			`UPDATE post_images SET caption = $1 WHERE id = $2 AND post_id = $3`,
			c, imageID, postID); err != nil {
			return fmt.Errorf("update gallery caption: %w", err)
		}
	}

	return nil
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
