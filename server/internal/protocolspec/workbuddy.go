package protocolspec

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// WorkBuddy (Tencent CodeBuddy) is served from two sites that share one API
// shape: the China site and the international site. Their hosts, origins and
// the endpoint paths live in protocol_specs.json under providers.workbuddy so
// no URL has to be hard-coded in the adapters, the login flow or the gateway.

// WorkBuddyRegion is one site (realm) an account can belong to.
type WorkBuddyRegion struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	LabelEN string   `json:"label_en"`
	BaseURL string   `json:"base_url"`
	Origin  string   `json:"origin"`
	Domains []string `json:"domains"`
	// Realms are the values the account export files use for this site.
	Realms []string `json:"realms"`
}

// WorkBuddyClient describes the client identity the upstream expects.
type WorkBuddyClient struct {
	Version     string `json:"version"`
	UserAgent   string `json:"user_agent"`
	Product     string `json:"product"`
	ProductCode string `json:"product_code"`
}

// WorkBuddyEndpoints are request paths relative to a region's base URL. The chat
// completions path is the provider's own `path`.
type WorkBuddyEndpoints struct {
	AuthState    string `json:"auth_state"`
	AuthToken    string `json:"auth_token"`
	TokenRefresh string `json:"token_refresh"`
	Account      string `json:"account"`
	Models       string `json:"models"`
	Billing      string `json:"billing"`
}

type WorkBuddySpec struct {
	DefaultRegion string             `json:"default_region"`
	Client        WorkBuddyClient    `json:"client"`
	Endpoints     WorkBuddyEndpoints `json:"endpoints"`
	Regions       []WorkBuddyRegion  `json:"regions"`
	// ChatPath is providers.workbuddy.path.
	ChatPath string `json:"-"`
}

type workBuddyProviderSpec struct {
	OfficialBaseURL string        `json:"official_base_url"`
	Path            string        `json:"path"`
	WorkBuddy       WorkBuddySpec `json:"workbuddy"`
}

var (
	workBuddyOnce sync.Once
	workBuddyData WorkBuddySpec
	workBuddyErr  error
)

// WorkBuddy returns the validated WorkBuddy spec.
func WorkBuddy() (WorkBuddySpec, error) {
	workBuddyOnce.Do(func() {
		var registry struct {
			Providers map[string]workBuddyProviderSpec `json:"providers"`
		}
		if err := json.Unmarshal(embeddedData, &registry); err != nil {
			workBuddyErr = fmt.Errorf("decode protocol specs: %w", err)
			return
		}
		provider, ok := registry.Providers["workbuddy"]
		if !ok {
			workBuddyErr = fmt.Errorf("protocol specs have no workbuddy provider")
			return
		}
		spec := provider.WorkBuddy
		spec.ChatPath = strings.TrimSpace(provider.Path)
		spec.Client.UserAgent = strings.ReplaceAll(spec.Client.UserAgent, "{version}", spec.Client.Version)
		if err := validateWorkBuddy(spec, provider.OfficialBaseURL); err != nil {
			workBuddyErr = fmt.Errorf("validate workbuddy spec: %w", err)
			return
		}
		workBuddyData = spec
	})
	return workBuddyData, workBuddyErr
}

// MustWorkBuddy returns the WorkBuddy spec. The embedded JSON is validated by
// the package tests, so a failure here is a build-time defect, not a runtime
// condition.
func MustWorkBuddy() WorkBuddySpec {
	spec, err := WorkBuddy()
	if err != nil {
		panic(err)
	}
	return spec
}

// Region looks a region up by id.
func (s WorkBuddySpec) Region(id string) (WorkBuddyRegion, bool) {
	id = normalize(id)
	for _, region := range s.Regions {
		if region.ID == id {
			return region, true
		}
	}
	return WorkBuddyRegion{}, false
}

// Default is the region used when an account does not record one.
func (s WorkBuddySpec) Default() WorkBuddyRegion {
	region, _ := s.Region(s.DefaultRegion)
	return region
}

// ResolveRegion maps the realm and/or tenant domain an account carries to a
// region. The realm wins; the domain is the fallback.
func (s WorkBuddySpec) ResolveRegion(realm string, domain string) (WorkBuddyRegion, bool) {
	realm = normalize(realm)
	domain = normalizeHost(domain)
	if realm != "" {
		for _, region := range s.Regions {
			if region.ID == realm || containsNormalized(region.Realms, realm) {
				return region, true
			}
		}
	}
	if domain != "" {
		for _, region := range s.Regions {
			if containsNormalized(region.Domains, domain) {
				return region, true
			}
		}
	}
	return WorkBuddyRegion{}, false
}

// RegionOrDefault resolves like ResolveRegion and falls back to the default
// region for accounts stored before regions were recorded.
func (s WorkBuddySpec) RegionOrDefault(realm string, domain string) WorkBuddyRegion {
	if region, ok := s.ResolveRegion(realm, domain); ok {
		return region
	}
	return s.Default()
}

// RegionForURL finds the region whose base URL host matches rawURL.
func (s WorkBuddySpec) RegionForURL(rawURL string) (WorkBuddyRegion, bool) {
	host := normalizeHost(rawURL)
	if host == "" {
		return WorkBuddyRegion{}, false
	}
	for _, region := range s.Regions {
		if normalizeHost(region.BaseURL) == host {
			return region, true
		}
	}
	return WorkBuddyRegion{}, false
}

func containsNormalized(values []string, wanted string) bool {
	for _, value := range values {
		if normalizeHost(value) == wanted || normalize(value) == wanted {
			return true
		}
	}
	return false
}

// normalizeHost lowercases a host or URL down to its hostname.
func normalizeHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	return strings.TrimSuffix(value, "/")
}

func validateWorkBuddy(spec WorkBuddySpec, officialBaseURL string) error {
	if len(spec.Regions) == 0 {
		return fmt.Errorf("no regions")
	}
	if !strings.HasPrefix(spec.ChatPath, "/") {
		return fmt.Errorf("provider path %q must start with /", spec.ChatPath)
	}
	if strings.TrimSpace(spec.Client.Version) == "" || strings.TrimSpace(spec.Client.UserAgent) == "" ||
		strings.TrimSpace(spec.Client.Product) == "" || strings.TrimSpace(spec.Client.ProductCode) == "" {
		return fmt.Errorf("client version, user_agent, product and product_code are required")
	}
	for name, path := range map[string]string{
		"auth_state":    spec.Endpoints.AuthState,
		"auth_token":    spec.Endpoints.AuthToken,
		"token_refresh": spec.Endpoints.TokenRefresh,
		"account":       spec.Endpoints.Account,
		"models":        spec.Endpoints.Models,
		"billing":       spec.Endpoints.Billing,
	} {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("endpoint %s %q must start with /", name, path)
		}
	}
	seen := map[string]string{}
	claim := func(kind string, value string, owner string) error {
		key := kind + ":" + value
		if previous, ok := seen[key]; ok {
			if previous == owner {
				return nil
			}
			return fmt.Errorf("%s %q is used by both %q and %q", kind, value, previous, owner)
		}
		seen[key] = owner
		return nil
	}
	for _, region := range spec.Regions {
		if region.ID == "" || region.ID != normalize(region.ID) {
			return fmt.Errorf("region id %q must be lowercase and non-empty", region.ID)
		}
		if _, dup := seen["region:"+region.ID]; dup {
			return fmt.Errorf("region id %q is defined twice", region.ID)
		}
		seen["region:"+region.ID] = region.ID
		for _, field := range []struct{ name, value string }{{"base_url", region.BaseURL}, {"origin", region.Origin}} {
			parsed, err := url.Parse(field.value)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || strings.TrimRight(parsed.Path, "/") != "" {
				return fmt.Errorf("region %q %s %q must be an https origin", region.ID, field.name, field.value)
			}
		}
		if strings.TrimSpace(region.Label) == "" || strings.TrimSpace(region.LabelEN) == "" {
			return fmt.Errorf("region %q needs label and label_en", region.ID)
		}
		if len(region.Domains) == 0 || len(region.Realms) == 0 {
			return fmt.Errorf("region %q needs domains and realms", region.ID)
		}
		for _, domain := range region.Domains {
			if err := claim("domain", normalizeHost(domain), region.ID); err != nil {
				return err
			}
		}
		for _, realm := range append([]string{region.ID}, region.Realms...) {
			if err := claim("realm", normalize(realm), region.ID); err != nil {
				return err
			}
		}
	}
	defaultRegion, ok := spec.Region(spec.DefaultRegion)
	if !ok {
		return fmt.Errorf("default_region %q is not a configured region", spec.DefaultRegion)
	}
	if strings.TrimRight(officialBaseURL, "/") != strings.TrimRight(defaultRegion.BaseURL, "/") {
		return fmt.Errorf("official_base_url %q must equal the default region base_url %q", officialBaseURL, defaultRegion.BaseURL)
	}
	return nil
}
