package site

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/adapter"
	oauthsvc "xlyra/server/internal/oauth"
	"xlyra/server/internal/protocolspec"
)

func workBuddySpec() protocolspec.WorkBuddySpec {
	return protocolspec.MustWorkBuddy()
}

// workBuddySiteConfig points an adapter site at the WorkBuddy site (China or
// international) the account lives on.
func workBuddySiteConfig(site adapter.SiteConfig, access oauthsvc.WorkBuddyAccess) adapter.SiteConfig {
	if access.Region.BaseURL != "" {
		site.BaseURL = access.Region.BaseURL
	}
	return site
}

// WorkBuddyRegions lists the sites an account can be logged in to, as
// configured in protocol_specs.json.
func WorkBuddyRegions() []protocolspec.WorkBuddyRegion {
	spec := workBuddySpec()
	return append([]protocolspec.WorkBuddyRegion(nil), spec.Regions...)
}

// WorkBuddyDefaultRegionID is the site used when none is chosen.
func WorkBuddyDefaultRegionID() string {
	return workBuddySpec().Default().ID
}

const (
	workBuddyImportMaxDocuments = 50
	workBuddyImportMaxAccounts  = 200
)

// WorkBuddyImportDocument is one uploaded account file.
type WorkBuddyImportDocument struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// WorkBuddyImportItem is the outcome for one account of an import.
type WorkBuddyImportItem struct {
	Source       string `json:"source"`
	Label        string `json:"label,omitempty"`
	Region       string `json:"region,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	Warning      string `json:"warning,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
}

// workBuddyImportRecord is the account data of an export file. Field names
// follow the exported JSON (snake_case); camelCase spellings are accepted too.
type workBuddyImportRecord struct {
	ID           string
	UID          string
	Nickname     string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Domain       string
	Realm        string
}

// ImportWorkBuddyAccounts stores the accounts of the uploaded files. Accounts
// that already exist (same identity) are updated in place. Every stored account
// is refreshed once so its models and quota are pulled and a dead token shows up
// immediately; a refresh failure is reported as a warning, not as a failed import.
func (s *Service) ImportWorkBuddyAccounts(ctx context.Context, siteID uuid.UUID, documents []WorkBuddyImportDocument) ([]WorkBuddyImportItem, error) {
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("no account files were provided")
	}
	if len(documents) > workBuddyImportMaxDocuments {
		return nil, fmt.Errorf("at most %d files can be imported at once", workBuddyImportMaxDocuments)
	}
	items := make([]WorkBuddyImportItem, 0, len(documents))
	total := 0
	for index, document := range documents {
		source := strings.TrimSpace(document.Name)
		if source == "" {
			source = "file-" + strconv.Itoa(index+1)
		}
		records, err := parseWorkBuddyImportDocument(document.Content)
		if err != nil {
			items = append(items, WorkBuddyImportItem{Source: source, Status: "failed", Error: err.Error()})
			continue
		}
		for _, record := range records {
			total++
			if total > workBuddyImportMaxAccounts {
				items = append(items, WorkBuddyImportItem{Source: source, Status: "failed", Error: fmt.Sprintf("at most %d accounts can be imported at once", workBuddyImportMaxAccounts)})
				break
			}
			items = append(items, s.importWorkBuddyRecord(ctx, siteID, source, record))
		}
	}
	return items, nil
}

func (s *Service) importWorkBuddyRecord(ctx context.Context, siteID uuid.UUID, source string, record workBuddyImportRecord) WorkBuddyImportItem {
	item := WorkBuddyImportItem{Source: source, Label: record.label()}
	tokens, err := record.tokens()
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		return item
	}
	item.Region = tokens.Realm
	account, err := s.CompleteWorkBuddyLogin(ctx, siteID, tokens, nil)
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		return item
	}
	item.Status = "imported"
	item.CredentialID = account.CredentialID.String()
	if refreshed, refreshErr := s.RefreshWorkBuddyAccount(ctx, siteID, account.CredentialID); refreshErr != nil {
		item.Warning = refreshErr.Error()
	} else if refreshed.MaskedToken != "" {
		item.Label = refreshed.MaskedToken
	}
	return item
}

func (r workBuddyImportRecord) label() string {
	return strings.TrimSpace(r.Nickname)
}

// tokens converts the record to login tokens, resolving the site the account
// belongs to from its realm and tenant domain.
func (r workBuddyImportRecord) tokens() (oauthsvc.WorkBuddyTokens, error) {
	if strings.TrimSpace(r.AccessToken) == "" {
		return oauthsvc.WorkBuddyTokens{}, fmt.Errorf("access_token is missing")
	}
	spec := workBuddySpec()
	region, ok := spec.ResolveRegion(r.Realm, r.Domain)
	if !ok {
		if strings.TrimSpace(r.Realm) != "" || strings.TrimSpace(r.Domain) != "" {
			return oauthsvc.WorkBuddyTokens{}, fmt.Errorf("unknown realm %q / domain %q", strings.TrimSpace(r.Realm), strings.TrimSpace(r.Domain))
		}
		region = spec.Default()
	}
	subject := strings.TrimSpace(r.UID)
	if subject == "" {
		subject = strings.TrimSpace(r.ID)
	}
	return oauthsvc.WorkBuddyTokens{
		AccessToken:  strings.TrimSpace(r.AccessToken),
		RefreshToken: strings.TrimSpace(r.RefreshToken),
		ExpiresAt:    r.ExpiresAt,
		Domain:       strings.TrimSpace(r.Domain),
		Realm:        region.ID,
		Email:        strings.TrimSpace(r.Nickname),
		Subject:      subject,
	}, nil
}

// parseWorkBuddyImportDocument reads one uploaded file: a JSON array of
// accounts, a single account object, or an object wrapping the array in
// "accounts" / "items" / "data".
func parseWorkBuddyImportDocument(content string) ([]workBuddyImportRecord, error) {
	content = strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	if content == "" {
		return nil, fmt.Errorf("the file is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(content)))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("the file is not valid JSON")
	}
	var rawItems []any
	switch value := root.(type) {
	case []any:
		rawItems = value
	case map[string]any:
		rawItems = []any{value}
		for _, key := range []string{"accounts", "items", "data"} {
			if wrapped, ok := value[key].([]any); ok {
				rawItems = wrapped
				break
			}
		}
	default:
		return nil, fmt.Errorf("the file must contain an account object or an array of accounts")
	}
	if len(rawItems) == 0 {
		return nil, fmt.Errorf("the file contains no accounts")
	}
	records := make([]workBuddyImportRecord, 0, len(rawItems))
	for _, raw := range rawItems {
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("every account must be a JSON object")
		}
		records = append(records, workBuddyRecordFromObject(object))
	}
	return records, nil
}

func workBuddyRecordFromObject(object map[string]any) workBuddyImportRecord {
	return workBuddyImportRecord{
		ID:           importString(object, "id"),
		UID:          importString(object, "uid", "user_id", "userId"),
		Nickname:     importString(object, "nickname", "nick_name", "name"),
		AccessToken:  importString(object, "access_token", "accessToken"),
		RefreshToken: importString(object, "refresh_token", "refreshToken"),
		ExpiresAt:    importTime(object["expires_at"], object["expiresAt"]),
		Domain:       importString(object, "domain"),
		Realm:        importString(object, "realm"),
	}
}

func importString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := object[key].(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		case json.Number:
			return value.String()
		}
	}
	return ""
}

// importTime reads an expiry given as epoch milliseconds, epoch seconds or an
// RFC 3339 string.
func importTime(values ...any) time.Time {
	for _, value := range values {
		switch typed := value.(type) {
		case json.Number:
			if number, err := typed.Float64(); err == nil && number > 0 {
				return epochToTime(number)
			}
		case string:
			text := strings.TrimSpace(typed)
			if text == "" {
				continue
			}
			if number, err := strconv.ParseFloat(text, 64); err == nil && number > 0 {
				return epochToTime(number)
			}
			if parsed, err := time.Parse(time.RFC3339, text); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

func epochToTime(value float64) time.Time {
	// Epoch seconds stay below 1e11 until the year 5138; anything larger is milliseconds.
	if value >= 1e11 {
		return time.UnixMilli(int64(value))
	}
	return time.Unix(int64(value), 0)
}
