package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func workBuddyTestJWT(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestWorkBuddyLoginFlow(t *testing.T) {
	polls := 0
	token := workBuddyTestJWT(map[string]any{"sub": "user-1", "email": "a@example.com"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "st-1", "authUrl": "https://login.example/auth?state=st-1"}})
		case "/v2/plugin/auth/token":
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "abc" {
				t.Errorf("poll must reuse the cookie session from the start request")
			}
			if r.URL.Query().Get("state") != "st-1" {
				t.Errorf("unexpected state %q", r.URL.Query().Get("state"))
			}
			polls++
			if polls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217, "msg": "login ing"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"accessToken": token, "refreshToken": "r-1", "expiresIn": 3600, "domain": "tenant.example",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	previous := workBuddyBaseURLOverride
	workBuddyBaseURLOverride = server.URL
	defer func() { workBuddyBaseURLOverride = previous }()

	svc := &Service{httpClient: server.Client()}
	start, err := svc.StartWorkBuddyLogin(context.Background(), uuid.Nil, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if start.State != "st-1" || start.AuthURL == "" || start.Region != "cn" {
		t.Fatalf("unexpected start: %+v", start)
	}
	if _, err := svc.PollWorkBuddyLogin(context.Background(), uuid.Nil, start.State); !errors.Is(err, ErrWorkBuddyAuthorizationPending) {
		t.Fatalf("expected pending, got %v", err)
	}
	tokens, err := svc.PollWorkBuddyLogin(context.Background(), uuid.Nil, start.State)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tokens.AccessToken != token || tokens.RefreshToken != "r-1" || tokens.Domain != "tenant.example" || tokens.Email != "a@example.com" || tokens.Subject != "user-1" || tokens.Realm != "cn" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	if time.Until(tokens.ExpiresAt) < 55*time.Minute {
		t.Fatalf("unexpected expiry: %v", tokens.ExpiresAt)
	}
	if _, err := svc.PollWorkBuddyLogin(context.Background(), uuid.Nil, start.State); err == nil {
		t.Fatal("finished login session must be consumed")
	}
}

func TestWorkBuddyRefresh(t *testing.T) {
	status := http.StatusOK
	body := map[string]any{"code": 0, "data": map[string]any{"accessToken": "new-token", "refreshToken": "r-2", "expiresIn": 7200}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Refresh-Token") != "r-1" {
			t.Errorf("missing refresh token header")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	previous := workBuddyBaseURLOverride
	workBuddyBaseURLOverride = server.URL
	defer func() { workBuddyBaseURLOverride = previous }()
	svc := &Service{httpClient: server.Client()}
	current := WorkBuddyCredentialBundle{AccessToken: "old", RefreshToken: "r-1", Domain: "tenant.example"}

	tokens, err := svc.RefreshWorkBuddyTokens(context.Background(), uuid.Nil, current)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tokens.AccessToken != "new-token" || tokens.RefreshToken != "r-2" || tokens.Domain != "tenant.example" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}

	status, body = http.StatusUnauthorized, map[string]any{"code": 401, "msg": "invalid refresh token"}
	if _, err := svc.RefreshWorkBuddyTokens(context.Background(), uuid.Nil, current); !errors.Is(err, ErrWorkBuddyReauthorizationRequired) {
		t.Fatalf("expected reauthorization error, got %v", err)
	}
	status = http.StatusBadGateway
	if _, err := svc.RefreshWorkBuddyTokens(context.Background(), uuid.Nil, current); err == nil || errors.Is(err, ErrWorkBuddyReauthorizationRequired) {
		t.Fatalf("5xx must stay a transient error, got %v", err)
	}
}

func TestWorkBuddyBundleFresh(t *testing.T) {
	if !workBuddyBundleFresh(WorkBuddyCredentialBundle{AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour).Unix()}) {
		t.Fatal("bundle with an hour left should be fresh")
	}
	if workBuddyBundleFresh(WorkBuddyCredentialBundle{AccessToken: "x", ExpiresAt: time.Now().Add(time.Minute).Unix()}) {
		t.Fatal("bundle inside the refresh lead window must be refreshed")
	}
	if workBuddyBundleFresh(WorkBuddyCredentialBundle{AccessToken: "x"}) {
		t.Fatal("unknown expiry must be refreshed")
	}
}

func TestWorkBuddyLoginUsesTheSelectedRegion(t *testing.T) {
	token := workBuddyTestJWT(map[string]any{"sub": "user-2"})
	var origins []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origins = append(origins, r.Header.Get("Origin"))
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "st-intl", "authUrl": "https://www.codebuddy.ai/login?state=st-intl"}})
		case "/v2/plugin/auth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"accessToken": token, "refreshToken": "r", "expiresIn": 3600}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	previous := workBuddyBaseURLOverride
	workBuddyBaseURLOverride = server.URL
	defer func() { workBuddyBaseURLOverride = previous }()

	svc := &Service{httpClient: server.Client()}
	start, err := svc.StartWorkBuddyLogin(context.Background(), uuid.Nil, "intl")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if start.Region != "intl" {
		t.Fatalf("region = %q, want intl", start.Region)
	}
	tokens, err := svc.PollWorkBuddyLogin(context.Background(), uuid.Nil, start.State)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tokens.Realm != "intl" {
		t.Fatalf("tokens realm = %q, want intl", tokens.Realm)
	}
	for _, origin := range origins {
		if origin != "https://www.codebuddy.ai" {
			t.Fatalf("origin = %q, want the international site", origin)
		}
	}
	if _, err := svc.StartWorkBuddyLogin(context.Background(), uuid.Nil, "mars"); err == nil {
		t.Fatal("an unknown region must be rejected")
	}
}

func TestWorkBuddyRefreshKeepsTheAccountRegion(t *testing.T) {
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin = r.Header.Get("Origin")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"accessToken": "next", "refreshToken": "r-2", "expiresIn": 3600}})
	}))
	defer server.Close()
	previous := workBuddyBaseURLOverride
	workBuddyBaseURLOverride = server.URL
	defer func() { workBuddyBaseURLOverride = previous }()
	svc := &Service{httpClient: server.Client()}

	tokens, err := svc.RefreshWorkBuddyTokens(context.Background(), uuid.Nil, WorkBuddyCredentialBundle{AccessToken: "old", RefreshToken: "r-1", Realm: "intl"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tokens.Realm != "intl" || origin != "https://www.codebuddy.ai" {
		t.Fatalf("realm=%q origin=%q, want the international site", tokens.Realm, origin)
	}
	// An account stored before regions were recorded stays on the default site.
	if _, err := svc.RefreshWorkBuddyTokens(context.Background(), uuid.Nil, WorkBuddyCredentialBundle{AccessToken: "old", RefreshToken: "r-1"}); err != nil {
		t.Fatalf("refresh without realm: %v", err)
	}
	if origin != "https://www.codebuddy.cn" {
		t.Fatalf("origin = %q, want the China site for accounts without a realm", origin)
	}
	// The tenant domain identifies the site when the realm is missing.
	if _, err := svc.RefreshWorkBuddyTokens(context.Background(), uuid.Nil, WorkBuddyCredentialBundle{AccessToken: "old", RefreshToken: "r-1", Domain: "www.codebuddy.ai"}); err != nil {
		t.Fatalf("refresh by domain: %v", err)
	}
	if origin != "https://www.codebuddy.ai" {
		t.Fatalf("origin = %q, want the international site resolved from the domain", origin)
	}
}

// Accounts of the China and the international site live side by side on one
// WorkBuddy site; each must be sent to its own host.
func TestWorkBuddyAccessPicksTheHostPerAccount(t *testing.T) {
	cases := []struct {
		name       string
		bundle     WorkBuddyCredentialBundle
		wantRegion string
		wantBase   string
	}{
		{name: "china realm", bundle: WorkBuddyCredentialBundle{AccessToken: "a", Realm: "cn", Domain: "www.codebuddy.cn"}, wantRegion: "cn", wantBase: "https://copilot.tencent.com"},
		{name: "international realm", bundle: WorkBuddyCredentialBundle{AccessToken: "b", Realm: "intl", Domain: "www.codebuddy.ai"}, wantRegion: "intl", wantBase: "https://www.codebuddy.ai"},
		{name: "international by domain", bundle: WorkBuddyCredentialBundle{AccessToken: "c", Domain: "www.codebuddy.ai"}, wantRegion: "intl", wantBase: "https://www.codebuddy.ai"},
		{name: "account stored before regions existed", bundle: WorkBuddyCredentialBundle{AccessToken: "d"}, wantRegion: "cn", wantBase: "https://copilot.tencent.com"},
		{name: "realm wins over a conflicting domain", bundle: WorkBuddyCredentialBundle{AccessToken: "e", Realm: "intl", Domain: "www.codebuddy.cn"}, wantRegion: "intl", wantBase: "https://www.codebuddy.ai"},
	}
	for _, tc := range cases {
		access := workBuddyAccessFromBundle(tc.bundle)
		if access.Token != tc.bundle.AccessToken || access.Region.ID != tc.wantRegion || access.Region.BaseURL != tc.wantBase {
			t.Fatalf("%s: access = token %q region %q base %q, want region %q base %q", tc.name, access.Token, access.Region.ID, access.Region.BaseURL, tc.wantRegion, tc.wantBase)
		}
		if access.Domain != tc.bundle.Domain {
			t.Fatalf("%s: domain = %q, want it passed through unchanged", tc.name, access.Domain)
		}
	}
}
