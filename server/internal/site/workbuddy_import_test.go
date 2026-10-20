package site

import (
	"strings"
	"testing"
	"time"
)

func TestParseWorkBuddyImportDocumentAcceptsExportShapes(t *testing.T) {
	t.Parallel()

	account := `{"id":"u-1","uid":"u-1","nickname":"Nick","access_token":"at","refresh_token":"rt","expires_at":1794469656000,"domain":"www.codebuddy.cn","token_type":"Bearer","realm":"cn"}`
	for name, content := range map[string]string{
		"array":         "[" + account + "]",
		"single object": account,
		"wrapped":       `{"accounts":[` + account + `]}`,
		"bom":           "\ufeff[" + account + "]",
	} {
		records, err := parseWorkBuddyImportDocument(content)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(records) != 1 {
			t.Fatalf("%s: got %d records, want 1", name, len(records))
		}
		record := records[0]
		if record.AccessToken != "at" || record.RefreshToken != "rt" || record.Realm != "cn" || record.Domain != "www.codebuddy.cn" || record.UID != "u-1" || record.Nickname != "Nick" {
			t.Fatalf("%s: record = %+v", name, record)
		}
		if !record.ExpiresAt.Equal(time.UnixMilli(1794469656000)) {
			t.Fatalf("%s: expires_at = %v, want epoch milliseconds", name, record.ExpiresAt)
		}
	}
}

func TestParseWorkBuddyImportDocumentRejectsBadInput(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"empty":         "  ",
		"not json":      "access_token=abc",
		"scalar":        `"text"`,
		"empty array":   "[]",
		"array of text": `["a"]`,
	} {
		if _, err := parseWorkBuddyImportDocument(content); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestWorkBuddyImportRecordTokensResolveTheRegion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		record  workBuddyImportRecord
		realm   string
		wantErr string
	}{
		{name: "china realm", record: workBuddyImportRecord{AccessToken: "t", Realm: "cn", Domain: "www.codebuddy.cn"}, realm: "cn"},
		{name: "international realm alias", record: workBuddyImportRecord{AccessToken: "t", Realm: "International"}, realm: "intl"},
		{name: "international by domain", record: workBuddyImportRecord{AccessToken: "t", Domain: "www.codebuddy.ai"}, realm: "intl"},
		{name: "no realm and no domain uses the default site", record: workBuddyImportRecord{AccessToken: "t"}, realm: "cn"},
		{name: "unknown realm", record: workBuddyImportRecord{AccessToken: "t", Realm: "mars"}, wantErr: "unknown realm"},
		{name: "missing access token", record: workBuddyImportRecord{Realm: "cn"}, wantErr: "access_token"},
	}
	for _, tt := range tests {
		tokens, err := tt.record.tokens()
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("%s: error = %v, want %q", tt.name, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if tokens.Realm != tt.realm {
			t.Fatalf("%s: realm = %q, want %q", tt.name, tokens.Realm, tt.realm)
		}
	}
}

func TestWorkBuddyImportRecordSubjectPrefersUID(t *testing.T) {
	t.Parallel()

	tokens, err := (workBuddyImportRecord{AccessToken: "t", ID: "id-1", UID: "uid-1", Nickname: " Nick "}).tokens()
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Subject != "uid-1" || tokens.Email != "Nick" {
		t.Fatalf("tokens = %+v", tokens)
	}
	tokens, _ = (workBuddyImportRecord{AccessToken: "t", ID: "id-1"}).tokens()
	if tokens.Subject != "id-1" {
		t.Fatalf("subject = %q, want the id fallback", tokens.Subject)
	}
}

func TestEpochToTimeAcceptsSecondsAndMilliseconds(t *testing.T) {
	t.Parallel()

	if got := epochToTime(1794469656); !got.Equal(time.Unix(1794469656, 0)) {
		t.Fatalf("seconds = %v", got)
	}
	if got := epochToTime(1794469656000); !got.Equal(time.UnixMilli(1794469656000)) {
		t.Fatalf("milliseconds = %v", got)
	}
}
