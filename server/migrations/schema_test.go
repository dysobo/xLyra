package migrations

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestEmbeddedMigrationsAreOrdered(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		t.Fatal("no embedded migrations")
	}
	sort.Strings(names)
	pattern := regexp.MustCompile(`^[0-9]{5}_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$`)
	for index, name := range names {
		if !pattern.MatchString(name) {
			t.Fatalf("migration %q does not follow the five-digit version naming convention", name)
		}
		parts := strings.SplitN(name, "_", 2)
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			t.Fatalf("migration %q has invalid version: %v", name, err)
		}
		expected := int64(index + 1)
		if version != expected {
			t.Fatalf("migration %q has version %d, want consecutive version %d", name, version, expected)
		}
		content, err := FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read migration %q: %v", name, err)
		}
		if !strings.Contains(string(content), "-- +goose Up") {
			t.Fatalf("migration %q is missing goose Up directive", name)
		}
	}
}
