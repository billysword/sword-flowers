package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/billysword/sword-flowers/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "sword flowers")
	})

	http.HandleFunc("/posts", postsHandler(pool))

	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func postsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(), "SELECT subject FROM posts ORDER BY created_at DESC")
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			log.Printf("posts query: %v", err)
			return
		}
		defer rows.Close()

		var subjects []string
		for rows.Next() {
			var subject string
			if err := rows.Scan(&subject); err != nil {
				http.Error(w, "scan failed", http.StatusInternalServerError)
				log.Printf("posts scan: %v", err)
				return
			}
			subjects = append(subjects, subject)
		}

		fmt.Fprintln(w, strings.Join(subjects, "\n"))
	}
}
