package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	sessionCookieName = "session"
	stateCookieName   = "oauth_state"
)

func newOAuthConfig(clientID, clientSecret, baseURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  baseURL + "/auth/callback",
		Scopes:       []string{"openid", "https://www.googleapis.com/auth/userinfo.email"},
		Endpoint:     google.Endpoint,
	}
}

func loginHandler(cfg *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		state := base64.URLEncoding.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name:     stateCookieName,
			Value:    state,
			Path:     "/",
			MaxAge:   300,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, cfg.AuthCodeURL(state), http.StatusFound)
	}
}

func callbackHandler(cfg *oauth2.Config, sessionSecret, allowedEmails, baseURL string) http.HandlerFunc {
	allowed := map[string]bool{}
	for _, e := range strings.Split(allowedEmails, ",") {
		if addr := strings.TrimSpace(strings.ToLower(e)); addr != "" {
			allowed[addr] = true
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// Verify state to guard against CSRF
		stateCookie, err := r.Cookie(stateCookieName)
		if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: stateCookieName, MaxAge: -1, Path: "/"})

		token, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}

		client := cfg.Client(r.Context(), token)
		resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
		if err != nil {
			http.Error(w, "userinfo request failed", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var info struct {
			Email    string `json:"email"`
			Verified bool   `json:"verified_email"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			http.Error(w, "userinfo decode failed", http.StatusInternalServerError)
			return
		}

		if !info.Verified || !allowed[strings.ToLower(info.Email)] {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}

		setSessionCookie(w, info.Email, sessionSecret, baseURL)
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
	}
}

func logoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, MaxAge: -1, Path: "/"})
		http.Redirect(w, r, "/posts", http.StatusSeeOther)
	}
}

// requireAdmin redirects unauthenticated requests to /auth/login.
func requireAdmin(sessionSecret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := getSessionEmail(r, sessionSecret); !ok {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func setSessionCookie(w http.ResponseWriter, email, secret, baseURL string) {
	value := base64.URLEncoding.EncodeToString([]byte(email)) + "." + cookieSig(secret, email)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   strings.HasPrefix(baseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func getSessionEmail(r *http.Request, secret string) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	emailBytes, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	email := string(emailBytes)
	if !hmac.Equal([]byte(cookieSig(secret, email)), []byte(parts[1])) {
		return "", false
	}
	return email, true
}

func cookieSig(secret, email string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(email))
	return base64.URLEncoding.EncodeToString(mac.Sum(nil))
}
