package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/protocolspec"
	"xlyra/server/internal/store"
)

// WorkBuddy (Tencent CodeBuddy) login, for the China site and the international
// site. Hosts, origins and request paths come from protocol_specs.json
// (providers.workbuddy), the accounts record which site they belong to.
//
// The CLI login is not a standard OAuth device flow:
//
//  1. POST /v2/plugin/auth/state?platform=CLI        -> {state, authUrl}
//  2. the user opens authUrl in a browser and logs in
//  3. GET  /v2/plugin/auth/token?state=<state>       -> code 11217 while pending,
//     code 0 + {accessToken, refreshToken, expiresIn, domain} once done
//  4. POST /v2/plugin/auth/token/refresh             (X-Refresh-Token header)
//
// Every response is wrapped in {code, msg, data}. The state is bound to a
// cookie session on the server, so start and poll must share a cookie jar.
const (
	workBuddyCodeLoginPending = 11217
	workBuddyLoginTTL         = 5 * time.Minute
	workBuddyPollInterval     = 3
	workBuddyRefreshLead      = 5 * time.Minute
	workBuddyResponseLimit    = 1 << 20
)

func workBuddySpec() protocolspec.WorkBuddySpec {
	return protocolspec.MustWorkBuddy()
}

// workBuddyBaseURLOverride lets tests point every region at a local server.
var workBuddyBaseURLOverride string

func workBuddyBaseURL(region protocolspec.WorkBuddyRegion) string {
	if workBuddyBaseURLOverride != "" {
		return workBuddyBaseURLOverride
	}
	return region.BaseURL
}

// ResolveWorkBuddyRegion maps a region id (empty selects the default) to its
// spec entry.
func ResolveWorkBuddyRegion(id string) (protocolspec.WorkBuddyRegion, error) {
	spec := workBuddySpec()
	if strings.TrimSpace(id) == "" {
		return spec.Default(), nil
	}
	region, ok := spec.ResolveRegion(id, "")
	if !ok {
		return protocolspec.WorkBuddyRegion{}, fmt.Errorf("unknown workbuddy region %q", id)
	}
	return region, nil
}

var (
	ErrWorkBuddyAuthorizationPending    = errors.New("workbuddy authorization pending")
	ErrWorkBuddyReauthorizationRequired = errors.New("workbuddy reauthorization required")
)

type WorkBuddyLoginStart struct {
	Region    string    `json:"region"`
	State     string    `json:"state"`
	AuthURL   string    `json:"auth_url"`
	Interval  int       `json:"interval"`
	ExpiresAt time.Time `json:"expires_at"`
}

type WorkBuddyTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Domain       string
	// Realm is the id of the WorkBuddy site (region) the account belongs to.
	Realm   string
	Email   string
	Subject string
}

type workBuddyLoginSession struct {
	client    *http.Client
	region    protocolspec.WorkBuddyRegion
	siteID    uuid.UUID
	expiresAt time.Time
}

// workBuddyLogins keeps in-flight browser logins (state -> cookie session).
// Sessions live for a few minutes only; a restart simply asks the user to
// start the login again.
var workBuddyLogins sync.Map

type workBuddyEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type workBuddyStateData struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}

type workBuddyTokenData struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresIn        int64  `json:"expiresIn"`
	RefreshExpiresIn int64  `json:"refreshExpiresIn"`
	Domain           string `json:"domain"`
}

func workBuddyRequestHeaders(req *http.Request, region protocolspec.WorkBuddyRegion) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", region.Origin)
	req.Header.Set("Referer", region.Origin+"/")
	req.Header.Set("User-Agent", workBuddySpec().Client.UserAgent)
}

// workBuddyDo executes one request and decodes the {code,msg,data} envelope.
// It returns the HTTP status alongside the envelope so callers can tell
// transport/gateway failures apart from application-level codes.
func workBuddyDo(client *http.Client, req *http.Request) (workBuddyEnvelope, int, error) {
	resp, err := client.Do(req)
	if err != nil {
		return workBuddyEnvelope{}, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, workBuddyResponseLimit))
	var envelope workBuddyEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			return workBuddyEnvelope{}, resp.StatusCode, fmt.Errorf("workbuddy upstream returned %d", resp.StatusCode)
		}
		return workBuddyEnvelope{}, resp.StatusCode, fmt.Errorf("decode workbuddy response: %w", err)
	}
	return envelope, resp.StatusCode, nil
}

// workBuddyLoginClient clones the site-scoped HTTP client (so the site proxy
// still applies) and gives it a private cookie jar.
func (s *Service) workBuddyLoginClient(ctx context.Context, siteID uuid.UUID) (*http.Client, error) {
	base, err := s.grokHTTPClient(ctx, siteID)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create workbuddy cookie jar: %w", err)
	}
	cloned := *base
	cloned.Jar = jar
	if cloned.Timeout == 0 || cloned.Timeout > 30*time.Second {
		cloned.Timeout = 30 * time.Second
	}
	return &cloned, nil
}

func (s *Service) workBuddyAPIClient(ctx context.Context, siteID uuid.UUID) (*http.Client, error) {
	return s.grokHTTPClient(ctx, siteID)
}

// StartWorkBuddyLogin begins a browser login against the given site; an empty
// regionID selects the default (China) site.
func (s *Service) StartWorkBuddyLogin(ctx context.Context, siteID uuid.UUID, regionID string) (WorkBuddyLoginStart, error) {
	region, err := ResolveWorkBuddyRegion(regionID)
	if err != nil {
		return WorkBuddyLoginStart{}, err
	}
	client, err := s.workBuddyLoginClient(ctx, siteID)
	if err != nil {
		return WorkBuddyLoginStart{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, workBuddyBaseURL(region)+workBuddySpec().Endpoints.AuthState, bytes.NewReader([]byte("{}")))
	if err != nil {
		return WorkBuddyLoginStart{}, fmt.Errorf("create workbuddy auth state request: %w", err)
	}
	workBuddyRequestHeaders(req, region)
	envelope, status, err := workBuddyDo(client, req)
	if err != nil {
		return WorkBuddyLoginStart{}, fmt.Errorf("request workbuddy auth state: %w", err)
	}
	if status < 200 || status >= 300 || envelope.Code != 0 {
		return WorkBuddyLoginStart{}, fmt.Errorf("workbuddy auth state returned status %d code %d: %s", status, envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	var data workBuddyStateData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return WorkBuddyLoginStart{}, fmt.Errorf("decode workbuddy auth state: %w", err)
	}
	data.State = strings.TrimSpace(data.State)
	data.AuthURL = strings.TrimSpace(data.AuthURL)
	if data.State == "" || data.AuthURL == "" {
		return WorkBuddyLoginStart{}, fmt.Errorf("workbuddy auth state response was incomplete")
	}
	expiresAt := time.Now().Add(workBuddyLoginTTL)
	workBuddyLogins.Store(data.State, &workBuddyLoginSession{client: client, region: region, siteID: siteID, expiresAt: expiresAt})
	pruneWorkBuddyLogins()
	return WorkBuddyLoginStart{
		Region:    region.ID,
		State:     data.State,
		AuthURL:   data.AuthURL,
		Interval:  workBuddyPollInterval,
		ExpiresAt: expiresAt,
	}, nil
}

func pruneWorkBuddyLogins() {
	now := time.Now()
	workBuddyLogins.Range(func(key, value any) bool {
		if session, ok := value.(*workBuddyLoginSession); ok && now.After(session.expiresAt) {
			workBuddyLogins.Delete(key)
		}
		return true
	})
}

// PollWorkBuddyLogin checks the login state once. It returns
// ErrWorkBuddyAuthorizationPending until the user finished the browser login.
func (s *Service) PollWorkBuddyLogin(ctx context.Context, siteID uuid.UUID, state string) (WorkBuddyTokens, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy login state is required")
	}
	value, ok := workBuddyLogins.Load(state)
	if !ok {
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy login session was not found or has expired, please start again")
	}
	session := value.(*workBuddyLoginSession)
	if session.siteID != siteID {
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy login session does not belong to this site")
	}
	if time.Now().After(session.expiresAt) {
		workBuddyLogins.Delete(state)
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy login expired, please start again")
	}
	endpoint := workBuddyBaseURL(session.region) + workBuddySpec().Endpoints.AuthToken + "?state=" + url.QueryEscape(state)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WorkBuddyTokens{}, fmt.Errorf("create workbuddy token request: %w", err)
	}
	workBuddyRequestHeaders(req, session.region)
	envelope, status, err := workBuddyDo(session.client, req)
	if err != nil {
		if status == 0 {
			return WorkBuddyTokens{}, fmt.Errorf("workbuddy token request: %w", err)
		}
		// Gateway-level rejections happen until the browser login finishes.
		return WorkBuddyTokens{}, ErrWorkBuddyAuthorizationPending
	}
	if envelope.Code == workBuddyCodeLoginPending || status >= http.StatusBadRequest {
		return WorkBuddyTokens{}, ErrWorkBuddyAuthorizationPending
	}
	if envelope.Code != 0 {
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy token request failed: code %d: %s", envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	var data workBuddyTokenData
	if err := json.Unmarshal(envelope.Data, &data); err != nil || strings.TrimSpace(data.AccessToken) == "" {
		return WorkBuddyTokens{}, ErrWorkBuddyAuthorizationPending
	}
	workBuddyLogins.Delete(state)
	tokens := workBuddyTokensFromData(time.Now(), data, "")
	tokens.Realm = session.region.ID
	return tokens, nil
}

// RefreshWorkBuddyTokens exchanges the refresh token for a new token bundle.
// Rejections by the upstream (HTTP 401/403 or an application error code) mean
// the refresh token is no longer valid and surface as
// ErrWorkBuddyReauthorizationRequired; network and 5xx failures stay transient.
func (s *Service) RefreshWorkBuddyTokens(ctx context.Context, siteID uuid.UUID, current WorkBuddyCredentialBundle) (WorkBuddyTokens, error) {
	client, err := s.workBuddyAPIClient(ctx, siteID)
	if err != nil {
		return WorkBuddyTokens{}, err
	}
	region := workBuddySpec().RegionOrDefault(current.Realm, current.Domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, workBuddyBaseURL(region)+workBuddySpec().Endpoints.TokenRefresh, bytes.NewReader([]byte("{}")))
	if err != nil {
		return WorkBuddyTokens{}, fmt.Errorf("create workbuddy refresh request: %w", err)
	}
	workBuddyRequestHeaders(req, region)
	req.Header.Set("X-Refresh-Token", strings.TrimSpace(current.RefreshToken))
	req.Header.Set("X-Auth-Refresh-Source", "workbuddy")
	if current.Domain != "" {
		req.Header.Set("X-Domain", current.Domain)
	}
	envelope, status, err := workBuddyDo(client, req)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return WorkBuddyTokens{}, fmt.Errorf("%w: upstream returned %d", ErrWorkBuddyReauthorizationRequired, status)
		}
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy refresh request: %w", err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || (status >= 200 && status < 300 && envelope.Code != 0) {
		return WorkBuddyTokens{}, fmt.Errorf("%w: status %d code %d: %s", ErrWorkBuddyReauthorizationRequired, status, envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	if status < 200 || status >= 300 {
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy refresh returned status %d", status)
	}
	var data workBuddyTokenData
	if err := json.Unmarshal(envelope.Data, &data); err != nil || strings.TrimSpace(data.AccessToken) == "" {
		return WorkBuddyTokens{}, fmt.Errorf("workbuddy refresh response missing accessToken")
	}
	tokens := workBuddyTokensFromData(time.Now(), data, current.Domain)
	tokens.Realm = region.ID
	return tokens, nil
}

func workBuddyTokensFromData(now time.Time, data workBuddyTokenData, fallbackDomain string) WorkBuddyTokens {
	tokens := WorkBuddyTokens{
		AccessToken:  strings.TrimSpace(data.AccessToken),
		RefreshToken: strings.TrimSpace(data.RefreshToken),
		Domain:       strings.TrimSpace(data.Domain),
	}
	if tokens.Domain == "" {
		tokens.Domain = strings.TrimSpace(fallbackDomain)
	}
	if data.ExpiresIn > 0 {
		tokens.ExpiresAt = now.Add(time.Duration(data.ExpiresIn) * time.Second)
	}
	if claims := workBuddyClaims(tokens.AccessToken); claims != nil {
		tokens.Email = firstNonEmptyClaim(claims, "email", "preferred_username", "name")
		tokens.Subject = firstNonEmptyClaim(claims, "sub", "uid", "user_id")
		if tokens.ExpiresAt.IsZero() {
			if exp, ok := claims["exp"].(float64); ok && exp > 0 {
				tokens.ExpiresAt = time.Unix(int64(exp), 0)
			}
		}
	}
	return tokens
}

func firstNonEmptyClaim(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := grokClaimString(claims, key); value != "" {
			return value
		}
	}
	return ""
}

func workBuddyClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

// WorkBuddyTokenIdentity returns a stable identity for de-duplicating accounts.
func WorkBuddyTokenIdentity(token string) string {
	return firstNonEmptyClaim(workBuddyClaims(token), "sub", "uid", "user_id")
}

type WorkBuddyCredentialBundle struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Domain       string `json:"domain,omitempty"`
	// Realm is the id of the WorkBuddy site (region); empty means the default
	// site, for accounts stored before regions were recorded.
	Realm string `json:"realm,omitempty"`
}

func WorkBuddyBundleFromTokens(t WorkBuddyTokens) WorkBuddyCredentialBundle {
	exp := int64(0)
	if !t.ExpiresAt.IsZero() {
		exp = t.ExpiresAt.Unix()
	}
	return WorkBuddyCredentialBundle{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    exp,
		Domain:       t.Domain,
		Realm:        t.Realm,
	}
}

func EncodeWorkBuddyBundle(b WorkBuddyCredentialBundle) (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("encode workbuddy credential bundle: %w", err)
	}
	return string(raw), nil
}

func DecodeWorkBuddyBundle(secret string) (WorkBuddyCredentialBundle, error) {
	var bundle WorkBuddyCredentialBundle
	if err := json.Unmarshal([]byte(strings.TrimSpace(secret)), &bundle); err != nil {
		return WorkBuddyCredentialBundle{}, fmt.Errorf("decode workbuddy credential bundle: %w", err)
	}
	return bundle, nil
}

func workBuddyBundleFresh(bundle WorkBuddyCredentialBundle) bool {
	expiresAt := bundle.ExpiresAt
	if expiresAt <= 0 {
		if exp, ok := workBuddyClaims(bundle.AccessToken)["exp"].(float64); ok {
			expiresAt = int64(exp)
		}
	}
	if expiresAt <= 0 {
		return false
	}
	return time.Until(time.Unix(expiresAt, 0)) > workBuddyRefreshLead
}

// WorkBuddyAccess is everything needed to call WorkBuddy as one account: the
// access token, the tenant domain (X-Domain) and the site the account lives on.
type WorkBuddyAccess struct {
	Token  string
	Domain string
	Region protocolspec.WorkBuddyRegion
}

// EnsureWorkBuddyAccess returns a valid access for one account credential,
// refreshing and persisting the tokens when needed.
func (s *Service) EnsureWorkBuddyAccess(ctx context.Context, credentialID uuid.UUID) (WorkBuddyAccess, error) {
	if s.db == nil {
		return WorkBuddyAccess{}, fmt.Errorf("oauth store is not available")
	}
	cred, err := store.NewSiteCredentialRepository(s.db.DB()).GetByID(ctx, credentialID)
	if err != nil {
		return WorkBuddyAccess{}, fmt.Errorf("load workbuddy credential: %w", err)
	}
	decrypted, err := s.credentials.Decrypt(cred.EncryptedSecret)
	if err != nil {
		return WorkBuddyAccess{}, fmt.Errorf("decrypt workbuddy credential: %w", err)
	}
	bundle, err := DecodeWorkBuddyBundle(decrypted)
	if err != nil {
		return WorkBuddyAccess{}, err
	}
	if bundle.AccessToken != "" && workBuddyBundleFresh(bundle) {
		return workBuddyAccessFromBundle(bundle), nil
	}
	if strings.TrimSpace(bundle.RefreshToken) == "" {
		if strings.TrimSpace(bundle.AccessToken) == "" {
			return WorkBuddyAccess{}, fmt.Errorf("workbuddy credential is missing tokens")
		}
		return WorkBuddyAccess{}, fmt.Errorf("%w: access token is expired or has unknown expiry and no refresh token is available", ErrWorkBuddyReauthorizationRequired)
	}
	result, err, _ := s.refreshGroup.Do("workbuddy:"+credentialID.String(), func() (any, error) {
		return s.refreshWorkBuddyCredentialOnce(ctx, cred.ID, cred.SiteID, bundle)
	})
	if err != nil {
		return WorkBuddyAccess{}, err
	}
	return workBuddyAccessFromBundle(result.(WorkBuddyCredentialBundle)), nil
}

func workBuddyAccessFromBundle(bundle WorkBuddyCredentialBundle) WorkBuddyAccess {
	return WorkBuddyAccess{
		Token:  bundle.AccessToken,
		Domain: bundle.Domain,
		Region: workBuddySpec().RegionOrDefault(bundle.Realm, bundle.Domain),
	}
}

func (s *Service) refreshWorkBuddyCredentialOnce(ctx context.Context, credentialID uuid.UUID, siteID uuid.UUID, current WorkBuddyCredentialBundle) (WorkBuddyCredentialBundle, error) {
	tokens, err := s.RefreshWorkBuddyTokens(ctx, siteID, current)
	if err != nil {
		return WorkBuddyCredentialBundle{}, fmt.Errorf("refresh workbuddy token: %w", err)
	}
	next := WorkBuddyBundleFromTokens(tokens)
	if strings.TrimSpace(next.RefreshToken) == "" {
		next.RefreshToken = current.RefreshToken
	}
	encoded, err := EncodeWorkBuddyBundle(next)
	if err != nil {
		return WorkBuddyCredentialBundle{}, err
	}
	encrypted, _, err := s.credentials.Encrypt(encoded)
	if err != nil {
		return WorkBuddyCredentialBundle{}, fmt.Errorf("encrypt workbuddy credential: %w", err)
	}
	if _, err := store.NewSiteCredentialRepository(s.db.DB()).UpdateSecret(ctx, credentialID, encrypted); err != nil {
		return WorkBuddyCredentialBundle{}, err
	}
	return next, nil
}
