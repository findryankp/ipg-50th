package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
)

const cookieName = "session_token"

type sessionStore struct {
	mu   sync.RWMutex
	data map[string]string // token -> nama pic
}

var sessions = &sessionStore{data: make(map[string]string)}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func createSession(w http.ResponseWriter, nama string) {
	token := newToken()
	sessions.mu.Lock()
	sessions.data[token] = nama
	sessions.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func currentPIC(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	sessions.mu.RLock()
	nama, ok := sessions.data[c.Value]
	sessions.mu.RUnlock()
	return nama, ok
}

func destroySession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		sessions.mu.Lock()
		delete(sessions.data, c.Value)
		sessions.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

// RequireLoginPage membungkus handler agar redirect ke /login jika belum login.
func RequireLoginPage(next func(w http.ResponseWriter, r *http.Request, pic string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pic, ok := currentPIC(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, pic)
	}
}
