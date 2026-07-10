package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const adminSessionCookie = "palrest_admin_session"

type adminAuth struct {
	username string
	password string
	ttl      time.Duration
	now      func() time.Time
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newAdminAuth(username, password string) *adminAuth {
	return &adminAuth{
		username: username,
		password: password,
		ttl:      12 * time.Hour,
		now:      time.Now,
		sessions: make(map[string]time.Time),
	}
}

func (a *adminAuth) enabled() bool {
	return a != nil && a.username != "" && a.password != ""
}

func (a *adminAuth) login(username, password string) (string, bool) {
	if !a.enabled() {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) != 1 {
		return "", false
	}
	token := randomToken()
	a.mu.Lock()
	a.sessions[token] = a.now().Add(a.ttl)
	a.mu.Unlock()
	return token, true
}

func (a *adminAuth) logout(token string) {
	if a == nil || token == "" {
		return
	}
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

func (a *adminAuth) valid(token string) bool {
	if !a.enabled() || token == "" {
		return false
	}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[token]
	if !ok {
		return false
	}
	if !now.Before(expires) {
		delete(a.sessions, token)
		return false
	}
	return true
}

func randomToken() string {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return newRequestID() + newRequestID()
	}
	return hex.EncodeToString(buffer)
}

func sessionCookie(token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     adminSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}
