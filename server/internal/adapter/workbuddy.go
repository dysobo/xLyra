package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xlyra/server/internal/protocolspec"
)

// WorkBuddy (Tencent CodeBuddy: China and international sites) site adapter.
//
// Accounts are connected through the CodeBuddy CLI browser login (see
// oauth/workbuddy.go); this adapter only describes the upstream: default base
// URL, model catalog, and the request headers the gateway has to attach.
const (
	WorkBuddySiteType      = "workbuddy"
	workBuddyResponseLimit = 4 << 20
)

// The site hosts (China / international), request paths and client identity
// come from protocol_specs.json (providers.workbuddy); see protocolspec.

func workBuddySpec() protocolspec.WorkBuddySpec {
	return protocolspec.MustWorkBuddy()
}

// WorkBuddyDefaultBaseURL is the base URL of the default region.
func WorkBuddyDefaultBaseURL() string {
	return workBuddySpec().Default().BaseURL
}

// WorkBuddyRegionForURL finds the region a request URL / base URL belongs to,
// falling back to the default region.
func WorkBuddyRegionForURL(rawURL string) protocolspec.WorkBuddyRegion {
	spec := workBuddySpec()
	if region, ok := spec.RegionForURL(rawURL); ok {
		return region
	}
	return spec.Default()
}

var ErrWorkBuddyUnauthorized = errors.New("workbuddy upstream unauthorized")

func IsWorkBuddyUnauthorizedError(err error) bool {
	return errors.Is(err, ErrWorkBuddyUnauthorized)
}

type WorkBuddy struct {
	client *http.Client
}

func NewWorkBuddy() WorkBuddy {
	return WorkBuddy{client: &http.Client{Timeout: 30 * time.Second}}
}

func (WorkBuddy) SiteTypes() []string {
	return []string{WorkBuddySiteType}
}

func (WorkBuddy) DefaultBaseURL() string {
	return WorkBuddyDefaultBaseURL()
}

func (WorkBuddy) Capabilities() []Capability {
	return []Capability{CapabilityHealthProbe, CapabilityValidateCredential, CapabilityListModels, CapabilitySummarizeAPIKey}
}

func (WorkBuddy) Scope() HealthProbeScope {
	return HealthProbeCredentialScope
}

func (a WorkBuddy) ProbeHealth(ctx context.Context, site SiteConfig, token string) ([]Model, error) {
	if _, err := a.fetchAccount(ctx, site, token); err != nil {
		return nil, err
	}
	return a.fetchModels(ctx, site, token)
}

func (a WorkBuddy) ValidateCredentials(ctx context.Context, site SiteConfig, token string) error {
	_, err := a.fetchAccount(ctx, site, token)
	return err
}

func (a WorkBuddy) ListModels(ctx context.Context, site SiteConfig, token string) ([]Model, error) {
	return a.fetchModels(ctx, site, token)
}

func (a WorkBuddy) SummarizeAPIKey(ctx context.Context, site SiteConfig, token string) (APIKeySummary, error) {
	account, err := a.fetchAccount(ctx, site, token)
	if err != nil {
		return APIKeySummary{}, err
	}
	models, err := a.fetchModels(ctx, site, token)
	if err != nil {
		return APIKeySummary{}, err
	}
	claims := workBuddyTokenClaims(token)
	label := firstNonEmptyString(
		anyString(account["nickname"]),
		anyString(account["phoneNumber"]),
		anyString(claims["email"]),
		anyString(claims["preferred_username"]),
	)
	accountType := firstNonEmptyString(anyString(account["type"]), "default")
	usage := map[string]any{
		"tier":         accountType,
		"email":        label,
		"quota_status": "unknown",
		"unlimited":    false,
	}
	raw := map[string]any{"account": account, "model_count": len(models)}
	// Quota is best-effort: a billing hiccup must not fail the account refresh.
	if quota, quotaErr := a.fetchQuota(ctx, site, token); quotaErr == nil {
		usage["quota_status"] = "ok"
		usage["limit"] = int64(math.Floor(quota.limit))
		usage["remaining"] = int64(math.Floor(quota.remaining))
		usage["used"] = int64(math.Ceil(quota.used))
		usage["unit"] = "credits"
		usage["packages"] = quota.packages
		raw["quota_packages"] = quota.packages
	} else if IsWorkBuddyUnauthorizedError(quotaErr) {
		return APIKeySummary{}, quotaErr
	} else {
		raw["quota_error"] = quotaErr.Error()
	}
	return APIKeySummary{
		Usage:  usage,
		Models: models,
		Raw:    raw,
	}, nil
}

type workBuddyEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func workBuddyEndpoint(site SiteConfig, path string) string {
	base := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if base == "" {
		base = WorkBuddyDefaultBaseURL()
	}
	return base + path
}

// getJSON performs an authenticated GET and unwraps the {code,msg,data} envelope.
func (a WorkBuddy) getJSON(ctx context.Context, site SiteConfig, token string, path string) (json.RawMessage, error) {
	return a.requestJSON(ctx, site, token, http.MethodGet, path, nil)
}

func (a WorkBuddy) requestJSON(ctx context.Context, site SiteConfig, token string, method string, path string, payload []byte) (json.RawMessage, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("workbuddy access token is required")
	}
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, workBuddyEndpoint(site, path), reader)
	if err != nil {
		return nil, fmt.Errorf("create workbuddy request: %w", err)
	}
	ApplyWorkBuddyUpstreamHeaders(req, token, "")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("workbuddy request %s failed: %w", path, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, workBuddyResponseLimit))
	if readErr != nil {
		return nil, fmt.Errorf("read workbuddy response: %w", readErr)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %s status %d", ErrWorkBuddyUnauthorized, path, resp.StatusCode)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("workbuddy %s returned status %d", path, resp.StatusCode)
	}
	var envelope workBuddyEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode workbuddy %s response: %w", path, err)
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("workbuddy %s returned code %d: %s", path, envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	return envelope.Data, nil
}

// fetchAccount calls the authenticated account endpoint; it is the cheapest
// call that proves the token is still accepted (the model config is public).
func (a WorkBuddy) fetchAccount(ctx context.Context, site SiteConfig, token string) (map[string]any, error) {
	data, err := a.getJSON(ctx, site, token, workBuddySpec().Endpoints.Account)
	if err != nil {
		return nil, err
	}
	account := map[string]any{}
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, fmt.Errorf("decode workbuddy account: %w", err)
	}
	return account, nil
}

// fetchModels reads the model catalog from the gateway config. The config lists
// every model the platform knows, including retired ones that the chat endpoint
// rejects with "service info not found"; the models attached to an agent are the
// ones actually routable, so the catalog is restricted to those when present.
// Image-only entries (no token limits) are skipped.
func (a WorkBuddy) fetchModels(ctx context.Context, site SiteConfig, token string) ([]Model, error) {
	data, err := a.getJSON(ctx, site, token, workBuddySpec().Endpoints.Models)
	if err != nil {
		return nil, err
	}
	var config struct {
		Agents []struct {
			Models []string `json:"models"`
		} `json:"agents"`
		Models []map[string]any `json:"models"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode workbuddy config: %w", err)
	}
	routable := map[string]struct{}{}
	for _, agent := range config.Agents {
		for _, id := range agent.Models {
			if id = strings.TrimSpace(id); id != "" {
				routable[id] = struct{}{}
			}
		}
	}
	models := make([]Model, 0, len(config.Models))
	seen := map[string]struct{}{}
	for _, item := range config.Models {
		id := anyString(item["id"])
		if id == "" || id == "default" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if _, hasLimit := item["maxInputTokens"]; !hasLimit {
			continue
		}
		if len(routable) > 0 {
			if _, ok := routable[id]; !ok {
				continue
			}
		}
		seen[id] = struct{}{}
		display := firstNonEmptyString(anyString(item["name"]), id)
		capabilities := map[string]any{
			"source":                   "workbuddy",
			"supported_endpoint_types": []string{"openai"},
			"raw":                      item,
		}
		if value, ok := workBuddyNumber(item["maxInputTokens"]); ok {
			capabilities["context_length"] = value
		}
		if value, ok := workBuddyNumber(item["maxOutputTokens"]); ok {
			capabilities["max_output_tokens"] = value
		}
		if vision, ok := item["supportsImages"].(bool); ok {
			capabilities["vision"] = vision
		}
		models = append(models, Model{UpstreamName: id, DisplayName: display, Capabilities: capabilities})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("workbuddy returned no models")
	}
	return models, nil
}

type workBuddyQuota struct {
	limit     float64
	remaining float64
	used      float64
	packages  []map[string]any
}

// workBuddyChinaZone is the zone the billing API formats cycle timestamps in.
var workBuddyChinaZone = time.FixedZone("CST", 8*3600)

// fetchQuota sums the credit packages of the account. Recurring packages
// (the cycle ends long before the package expires) report the balance of the
// current cycle; one-off packages (e.g. welcome gifts) report their total
// balance.
func (a WorkBuddy) fetchQuota(ctx context.Context, site SiteConfig, token string) (workBuddyQuota, error) {
	now := time.Now().In(workBuddyChinaZone)
	payload, err := json.Marshal(map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              workBuddySpec().Client.ProductCode,
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   now.AddDate(101, 0, 0).Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return workBuddyQuota{}, err
	}
	data, err := a.requestJSON(ctx, site, token, http.MethodPost, workBuddySpec().Endpoints.Billing, payload)
	if err != nil {
		return workBuddyQuota{}, err
	}
	var envelope struct {
		Response struct {
			Data struct {
				Accounts []map[string]any `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return workBuddyQuota{}, fmt.Errorf("decode workbuddy billing: %w", err)
	}
	quota := workBuddyQuota{packages: []map[string]any{}}
	for _, account := range envelope.Response.Data.Accounts {
		recurring := false
		if cycleEnd, err := time.ParseInLocation("2006-01-02 15:04:05", anyString(account["CycleEndTime"]), workBuddyChinaZone); err == nil {
			if deductionEnd, ok := workBuddyNumber(account["DeductionEndTime"]); ok {
				recurring = time.UnixMilli(deductionEnd).Sub(cycleEnd) > 24*time.Hour
			}
		}
		sizeKey, remainKey := "CapacitySizePrecise", "CapacityRemainPrecise"
		if recurring {
			sizeKey, remainKey = "CycleCapacitySizePrecise", "CycleCapacityRemainPrecise"
		}
		size, sizeOK := workBuddyFloat(account[sizeKey])
		remaining, remainOK := workBuddyFloat(account[remainKey])
		if !sizeOK || !remainOK {
			continue
		}
		quota.limit += size
		quota.remaining += remaining
		quota.used += size - remaining
		quota.packages = append(quota.packages, map[string]any{
			"name":       anyString(account["PackageName"]),
			"size":       size,
			"remaining":  remaining,
			"recurring":  recurring,
			"cycle_end":  anyString(account["CycleEndTime"]),
			"expires_at": workBuddyMillis(account["DeductionEndTime"]),
		})
	}
	if len(quota.packages) == 0 {
		return workBuddyQuota{}, fmt.Errorf("workbuddy billing returned no credit packages")
	}
	return quota, nil
}

func workBuddyFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	}
	return 0, false
}

func workBuddyMillis(value any) string {
	if millis, ok := workBuddyNumber(value); ok && millis > 0 {
		return time.UnixMilli(millis).UTC().Format(time.RFC3339)
	}
	return ""
}

func workBuddyNumber(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	return parsed, err == nil
}

// workBuddyTokenClaims decodes the (unverified) payload of a JWT access token.
// It is only used for display metadata such as the account label.
func workBuddyTokenClaims(token string) map[string]any {
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

// ApplyWorkBuddyUpstreamHeaders attaches the headers the CodeBuddy gateway
// expects. The Origin follows the site the request is sent to (China or
// international); domain is the tenant domain returned at login (may be empty).
func ApplyWorkBuddyUpstreamHeaders(req *http.Request, token string, domain string) {
	spec := workBuddySpec()
	region := spec.Default()
	if req.URL != nil {
		region = WorkBuddyRegionForURL(req.URL.String())
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", region.Origin)
	req.Header.Set("Referer", region.Origin+"/")
	req.Header.Set("User-Agent", spec.Client.UserAgent)
	req.Header.Set("X-Product", spec.Client.Product)
	if domain = strings.TrimSpace(domain); domain != "" {
		req.Header.Set("X-Domain", domain)
	} else {
		req.Header.Set("X-No-Department-Info", "1")
	}
}

// IsWorkBuddyURL reports whether the URL points at one of the WorkBuddy sites.
func IsWorkBuddyURL(value *url.URL) bool {
	if value == nil || !strings.EqualFold(value.Scheme, "https") {
		return false
	}
	_, ok := workBuddySpec().RegionForURL(value.String())
	return ok
}
