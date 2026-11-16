package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCorpusFiltersBeforeModelContext(t *testing.T) {
	store, err := Load(filepath.Join("..", "..", "data", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	at := moscowTime(2026, time.November, 15, 12, 0)
	context := store.EligibleContext("intern", "backend-platform-demo", at, true)
	ids := contextIDs(context)
	for _, id := range []string{"K-101", "K-102", "K-201", "K-202", "S-002"} {
		if !ids[id] {
			t.Errorf("approved record %s is missing", id)
		}
	}
	for _, id := range []string{"K-103", "K-104"} {
		if ids[id] {
			t.Errorf("unapproved record %s reached model context", id)
		}
	}
	for _, record := range context {
		if record.ID == "S-002" && record.AnswerScope != "existence_only" {
			t.Errorf("service scope was lost: %q", record.AnswerScope)
		}
	}
	if got := store.EligibleContext("intern", "backend-platform-demo", at, false); len(got) != 0 {
		t.Fatalf("production mode exposed %d demo records", len(got))
	}
}

func TestStoreLookupAndReviewContext(t *testing.T) {
	store, err := Load(filepath.Join("..", "..", "data", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if store.SnapshotID() == "" {
		t.Fatal("snapshot ID is empty")
	}
	if _, ok := store.RecordByID("K-101"); !ok {
		t.Fatal("known record was not found")
	}
	if _, ok := store.RecordByID("missing"); ok {
		t.Fatal("unknown record was found")
	}
	var nilStore *Store
	if _, ok := nilStore.RecordByID("K-101"); ok {
		t.Fatal("nil store returned a record")
	}
	if got := nilStore.ReviewContext("mentor", "backend-platform-demo"); got != nil {
		t.Fatalf("nil store returned review context: %+v", got)
	}
	if got := store.ReviewContext("unknown-role", "backend-platform-demo"); got != nil {
		t.Fatalf("unknown role received review context: %+v", got)
	}
	review := store.ReviewContext("mentor", "backend-platform-demo")
	if len(review) == 0 {
		t.Fatal("mentor review context is empty")
	}
	for _, record := range review {
		if record.AnswerScope != "" || record.Source.URI != "" {
			t.Fatalf("review context exposed answer-only metadata: %+v", record)
		}
		if _, ok := store.RecordByID(record.ID); !ok {
			t.Fatalf("review context contains unknown record %q", record.ID)
		}
	}
	for _, record := range store.ReviewContext("intern", "another-team") {
		stored, ok := store.RecordByID(record.ID)
		if !ok || (stored.TeamScope != "all-demo-teams" && stored.TeamScope != "another-team") {
			t.Fatalf("team-specific review context leaked: %+v", record)
		}
	}
}

func TestRoleTeamAndExpiryBoundary(t *testing.T) {
	file := fixture(t)
	for i := range file.Records {
		if file.Records[i].ID == "K-101" {
			file.Records[i].AllowedRoles = []string{"intern"}
		}
		if file.Records[i].ID == "K-201" {
			file.Records[i].ApprovalScope = "verified"
		}
	}
	store, err := Load(writeFixture(t, file))
	if err != nil {
		t.Fatal(err)
	}
	lastMinute := moscowTime(2026, time.December, 14, 23, 59)
	if !contextIDs(store.EligibleContext("intern", "backend-platform-demo", lastMinute, true))["K-101"] {
		t.Fatal("record expired before the end of its expiry date")
	}
	firstMinute := moscowTime(2026, time.December, 15, 0, 0)
	if contextIDs(store.EligibleContext("intern", "backend-platform-demo", firstMinute, true))["K-101"] {
		t.Fatal("expired record reached model context")
	}
	if contextIDs(store.EligibleContext("manager", "backend-platform-demo", lastMinute, true))["K-101"] {
		t.Fatal("record reached a role not listed in allowed_roles")
	}
	if contextIDs(store.EligibleContext("intern", "another-team", lastMinute, true))["K-101"] {
		t.Fatal("team-specific record reached another team")
	}
	production := contextIDs(store.EligibleContext("intern", "backend-platform-demo", lastMinute, false))
	if !production["K-201"] || production["K-101"] {
		t.Fatalf("verified and demo-only records were not separated: %v", production)
	}
	if got := store.EligibleContext("unknown-role", "backend-platform-demo", lastMinute, true); len(got) != 0 {
		t.Fatal("unknown role received knowledge")
	}
	if got := store.EligibleContext("intern", "backend-platform-demo", time.Time{}, true); len(got) != 0 {
		t.Fatal("missing request time received knowledge")
	}
}

func TestLoadRejectsUnsafeKnowledge(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fileData)
	}{
		{"duplicate ID", func(f *fileData) { f.Records = append(f.Records, f.Records[0]) }},
		{"unrecognized status", func(f *fileData) { f.Records[0].Status = "published" }},
		{"mismatched TTL", func(f *fileData) { f.Records[0].ExpiresAt = "2027-01-01" }},
		{"missing evidence", func(f *fileData) { f.Records[0].EvidenceText = "" }},
		{"synthetic marked verified", func(f *fileData) { f.Records[0].ApprovalScope = "verified" }},
		{"public source without URI", func(f *fileData) {
			for i := range f.Records {
				if f.Records[i].ID == "K-201" {
					f.Records[i].Source.URI = ""
				}
			}
		}},
		{"invalid role", func(f *fileData) { f.Records[0].AllowedRoles = []string{"guest"} }},
		{"changed model payload", func(f *fileData) { f.ModelContextFields = []string{"id", "answer"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := fixture(t)
			test.change(&file)
			if _, err := Load(writeFixture(t, file)); err == nil {
				t.Fatal("unsafe knowledge file was accepted")
			}
		})
	}
}

func TestLoadRejectsUnknownFieldsAndExtraJSON(t *testing.T) {
	file := fixture(t)
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data string
	}{
		{"unknown field", strings.Replace(string(data), `"schema_version":`, `"unexpected":true,"schema_version":`, 1)},
		{"extra object", string(data) + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "knowledge.json")
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("invalid JSON structure was accepted")
			}
		})
	}
}

func fixture(t *testing.T) fileData {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "data", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file fileData
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func writeFixture(t *testing.T, file fileData) string {
	t.Helper()
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "knowledge.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func contextIDs(records []ContextRecord) map[string]bool {
	ids := make(map[string]bool, len(records))
	for _, record := range records {
		ids[record.ID] = true
	}
	return ids
}

func moscowTime(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.FixedZone("MSK", 3*60*60))
}
