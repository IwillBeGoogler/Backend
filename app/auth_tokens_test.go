package app_test

import (
	"strings"
	"testing"
	"time"
)

func TestAccessAndRefreshTokens(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	anon.register("tok")

	var s struct {
		AccessToken      string    `json:"access_token"`
		ExpiresAt        time.Time `json:"expires_at"`
		RefreshToken     string    `json:"refresh_token"`
		RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	}
	anon.do("POST", "/api/auth/login", map[string]string{"login": "tok", "password": "secret123"}, 200, &s)
	if !strings.HasPrefix(s.AccessToken, "eyJ") || s.RefreshToken == "" || strings.HasPrefix(s.RefreshToken, "eyJ") {
		t.Fatalf("login body = %+v", s)
	}
	if d := time.Until(s.ExpiresAt); d < 2*time.Hour+50*time.Minute || d > 3*time.Hour+time.Minute {
		t.Errorf("access expires in %v, want 3h", d)
	}
	if d := time.Until(s.RefreshExpiresAt); d < 29*24*time.Hour {
		t.Errorf("refresh expires in %v, want 30 days", d)
	}
	loginAccess, firstRefresh := s.AccessToken, s.RefreshToken

	// Refresh: body or Bearer; rotates; an access token is not a refresh token.
	anon.do("POST", "/api/auth/refresh", map[string]string{"refresh_token": firstRefresh}, 200, &s)
	anon.do("POST", "/api/auth/refresh", map[string]string{"refresh_token": firstRefresh}, 401, nil)
	(&client{t: t, base: anon.base, token: s.AccessToken}).do("POST", "/api/auth/refresh", nil, 401, nil)
	(&client{t: t, base: anon.base, token: s.RefreshToken}).do("POST", "/api/auth/refresh", nil, 200, &s)

	// Logout ends the refresh token and every access token of the session.
	b := &client{t: t, base: anon.base, token: s.AccessToken}
	b.do("GET", "/api/me", nil, 200, nil)
	b.do("POST", "/api/auth/logout", map[string]string{"refresh_token": s.RefreshToken}, 204, nil)
	b.do("GET", "/api/me", nil, 401, nil)
	(&client{t: t, base: anon.base, token: loginAccess}).do("GET", "/api/me", nil, 401, nil)
	anon.do("POST", "/api/auth/refresh", map[string]string{"refresh_token": s.RefreshToken}, 401, nil)

	// Signing out everywhere ends other devices' access tokens too.
	d1 := anon.login("tok", "secret123")
	d2 := anon.login("tok", "secret123")
	d1.do("POST", "/api/auth/logout-all", nil, 204, nil)
	d2.do("GET", "/api/me", nil, 401, nil)
}
