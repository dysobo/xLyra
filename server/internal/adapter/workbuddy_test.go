package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const workBuddyConfigFixture = `{"code":0,"msg":"OK","data":{
  "agents":[{"name":"cli","models":["glm-5.3","kimi-k2.7","hy3"]}],
  "models":[
    {"id":"default","name":"Default","maxInputTokens":200000},
    {"id":"glm-5.3","name":"GLM-5.3","maxInputTokens":1000000,"maxOutputTokens":64000,"supportsImages":true,"reasoning":{"defaultEffort":"max"}},
    {"id":"glm-5.0","name":"GLM-5.0","maxInputTokens":200000},
    {"id":"kimi-k2.7","name":"Kimi-K2.7-Code","maxInputTokens":256000,"maxOutputTokens":32000},
    {"id":"hunyuan-image-alpha","name":"Hunyuan Image Alpha","tags":["text-to-image"]},
    {"id":"hy3","name":"Hy3","maxInputTokens":192000}
  ]}}`

// A recurring free package (cycle ends long before the package expires) and a
// one-off welcome gift (cycle end == expiry).
const workBuddyBillingFixture = `{"code":0,"data":{"Response":{"Data":{"Accounts":[
  {"PackageName":"Free","CycleEndTime":"2026-10-31 23:59:59","DeductionEndTime":2052737789000,
   "CapacitySizePrecise":"500","CapacityRemainPrecise":"500","CycleCapacitySizePrecise":"500","CycleCapacityRemainPrecise":"499.5"},
  {"PackageName":"Gift","CycleEndTime":"2026-11-18 20:56:30","DeductionEndTime":1795006590000,
   "CapacitySizePrecise":"1500","CapacityRemainPrecise":"1500","CycleCapacitySizePrecise":"1500","CycleCapacityRemainPrecise":"1500"}
]}}}}`

func TestWorkBuddyFetchesModelsFromUpstreamConfig(t *testing.T) {
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v3/config":
			_, _ = w.Write([]byte(workBuddyConfigFixture))
		case "/v2/billing/meter/get-user-resource":
			if r.Method != http.MethodPost {
				t.Errorf("billing must be POST, got %s", r.Method)
			}
			_, _ = w.Write([]byte(workBuddyBillingFixture))
		case "/v2/plugin/login/account":
			_, _ = w.Write([]byte(`{"code":0,"data":{"uid":"u1","nickname":"188****9521","type":"personal"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	site := SiteConfig{BaseURL: server.URL, Client: server.Client()}
	models, err := NewWorkBuddy().ListModels(context.Background(), site, "tok")
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if sawAuth != "Bearer tok" {
		t.Fatalf("unexpected auth header %q", sawAuth)
	}
	var ids []string
	for _, model := range models {
		ids = append(ids, model.UpstreamName)
	}
	want := []string{"glm-5.3", "kimi-k2.7", "hy3"}
	if len(ids) != len(want) {
		t.Fatalf("models = %v, want %v (virtual, unrouted and image models must be skipped)", ids, want)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("models = %v, want %v", ids, want)
		}
	}
	glm := models[0]
	if glm.DisplayName != "GLM-5.3" || glm.Capabilities["context_length"] != int64(1000000) || glm.Capabilities["max_output_tokens"] != int64(64000) || glm.Capabilities["vision"] != true {
		t.Fatalf("unexpected capabilities: %#v", glm.Capabilities)
	}

	summary, err := NewWorkBuddy().SummarizeAPIKey(context.Background(), site, "tok")
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	usage := summary.Usage.(map[string]any)
	if usage["email"] != "188****9521" || usage["tier"] != "personal" || len(summary.Models) != 3 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if usage["quota_status"] != "ok" || usage["limit"] != int64(2000) || usage["remaining"] != int64(1999) || usage["used"] != int64(1) {
		t.Fatalf("unexpected quota: %#v", usage)
	}
	if packages, _ := usage["packages"].([]map[string]any); len(packages) != 2 || packages[0]["recurring"] != true || packages[1]["recurring"] != false {
		t.Fatalf("unexpected packages: %#v", usage["packages"])
	}
}

func TestWorkBuddyRejectedTokenIsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "401 Authorization Required", http.StatusUnauthorized)
	}))
	defer server.Close()
	site := SiteConfig{BaseURL: server.URL, Client: server.Client()}
	err := NewWorkBuddy().ValidateCredentials(context.Background(), site, "bad")
	if !errors.Is(err, ErrWorkBuddyUnauthorized) || !IsWorkBuddyUnauthorizedError(err) {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestWorkBuddyHeadersFollowTheSiteOfTheRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url        string
		wantOrigin string
	}{
		{url: "https://copilot.tencent.com/v2/chat/completions", wantOrigin: "https://www.codebuddy.cn"},
		{url: "https://www.codebuddy.ai/v2/chat/completions", wantOrigin: "https://www.codebuddy.ai"},
		{url: "https://unknown.example/v2/chat/completions", wantOrigin: "https://www.codebuddy.cn"},
	}
	for _, tt := range tests {
		req, err := http.NewRequest(http.MethodPost, tt.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		ApplyWorkBuddyUpstreamHeaders(req, "token", "")
		if got := req.Header.Get("Origin"); got != tt.wantOrigin {
			t.Fatalf("%s: Origin = %q, want %q", tt.url, got, tt.wantOrigin)
		}
		if got := req.Header.Get("Referer"); got != tt.wantOrigin+"/" {
			t.Fatalf("%s: Referer = %q", tt.url, got)
		}
		if req.Header.Get("User-Agent") == "" || req.Header.Get("X-Product") == "" {
			t.Fatalf("%s: client identity headers are missing", tt.url)
		}
	}
}

func TestWorkBuddyBaseURLsComeFromTheSpec(t *testing.T) {
	t.Parallel()

	if got := (WorkBuddy{}).DefaultBaseURL(); got != "https://copilot.tencent.com" {
		t.Fatalf("default base url = %q", got)
	}
	if region := WorkBuddyRegionForURL("https://www.codebuddy.ai"); region.ID != "intl" {
		t.Fatalf("region = %q, want intl", region.ID)
	}
	for _, raw := range []string{"https://copilot.tencent.com/x", "https://www.codebuddy.ai"} {
		parsed, _ := url.Parse(raw)
		if !IsWorkBuddyURL(parsed) {
			t.Fatalf("%s should be a WorkBuddy URL", raw)
		}
	}
	other, _ := url.Parse("https://example.com")
	if IsWorkBuddyURL(other) {
		t.Fatal("example.com is not a WorkBuddy URL")
	}
}
