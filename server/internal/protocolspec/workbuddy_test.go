package protocolspec

import "testing"

func TestWorkBuddySpecIsValidAndNormalized(t *testing.T) {
	t.Parallel()

	spec, err := WorkBuddy()
	if err != nil {
		t.Fatalf("WorkBuddy() error = %v", err)
	}
	if len(spec.Regions) < 2 {
		t.Fatalf("regions = %d, want the China and international sites", len(spec.Regions))
	}
	if got := spec.Default().ID; got != "cn" {
		t.Fatalf("default region = %q, want cn", got)
	}
	cn, _ := spec.Region("cn")
	intl, _ := spec.Region("intl")
	if cn.BaseURL != "https://copilot.tencent.com" || intl.BaseURL != "https://www.codebuddy.ai" {
		t.Fatalf("base urls = %q / %q", cn.BaseURL, intl.BaseURL)
	}
	if spec.ChatPath != "/v2/chat/completions" || spec.Endpoints.AuthState == "" {
		t.Fatalf("paths = %q %+v", spec.ChatPath, spec.Endpoints)
	}
	if spec.Client.UserAgent != "CLI/"+spec.Client.Version+" CodeBuddy/"+spec.Client.Version {
		t.Fatalf("user agent = %q", spec.Client.UserAgent)
	}
}

func TestWorkBuddyResolveRegion(t *testing.T) {
	t.Parallel()

	spec := MustWorkBuddy()
	tests := []struct {
		name   string
		realm  string
		domain string
		want   string
		ok     bool
	}{
		{name: "realm cn", realm: "cn", want: "cn", ok: true},
		{name: "realm alias", realm: " Global ", want: "intl", ok: true},
		{name: "domain only", domain: "www.codebuddy.ai", want: "intl", ok: true},
		{name: "domain url", domain: "https://www.codebuddy.cn/", want: "cn", ok: true},
		{name: "realm wins over domain", realm: "intl", domain: "www.codebuddy.cn", want: "intl", ok: true},
		{name: "unknown", realm: "mars", domain: "example.com", ok: false},
		{name: "empty", ok: false},
	}
	for _, tt := range tests {
		region, ok := spec.ResolveRegion(tt.realm, tt.domain)
		if ok != tt.ok || (ok && region.ID != tt.want) {
			t.Fatalf("%s: ResolveRegion() = %q,%v want %q,%v", tt.name, region.ID, ok, tt.want, tt.ok)
		}
	}
	if got := spec.RegionOrDefault("", "").ID; got != "cn" {
		t.Fatalf("RegionOrDefault() = %q, want cn", got)
	}
}

func TestWorkBuddyRegionForURL(t *testing.T) {
	t.Parallel()

	spec := MustWorkBuddy()
	if region, ok := spec.RegionForURL("https://www.codebuddy.ai/v2/chat/completions"); !ok || region.ID != "intl" {
		t.Fatalf("RegionForURL(intl) = %q,%v", region.ID, ok)
	}
	if region, ok := spec.RegionForURL("https://copilot.tencent.com"); !ok || region.ID != "cn" {
		t.Fatalf("RegionForURL(cn) = %q,%v", region.ID, ok)
	}
	if _, ok := spec.RegionForURL("https://example.com"); ok {
		t.Fatal("unknown host must not resolve")
	}
}

func TestValidateWorkBuddyRejectsBrokenSpecs(t *testing.T) {
	t.Parallel()

	base := MustWorkBuddy()
	clone := func(mutate func(*WorkBuddySpec)) WorkBuddySpec {
		spec := base
		spec.Regions = append([]WorkBuddyRegion(nil), base.Regions...)
		mutate(&spec)
		return spec
	}
	official := base.Default().BaseURL
	cases := map[string]WorkBuddySpec{
		"unknown default":   clone(func(s *WorkBuddySpec) { s.DefaultRegion = "mars" }),
		"duplicate domain":  clone(func(s *WorkBuddySpec) { s.Regions[1].Domains = []string{"www.codebuddy.cn"} }),
		"duplicate realm":   clone(func(s *WorkBuddySpec) { s.Regions[1].Realms = []string{"cn", "intl"} }),
		"non https origin":  clone(func(s *WorkBuddySpec) { s.Regions[0].Origin = "http://www.codebuddy.cn" }),
		"relative endpoint": clone(func(s *WorkBuddySpec) { s.Endpoints.Account = "v2/account" }),
		"no regions":        clone(func(s *WorkBuddySpec) { s.Regions = nil }),
	}
	for name, spec := range cases {
		if err := validateWorkBuddy(spec, official); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
	if err := validateWorkBuddy(base, "https://elsewhere.example"); err == nil {
		t.Fatal("official_base_url that differs from the default region must be rejected")
	}
}
