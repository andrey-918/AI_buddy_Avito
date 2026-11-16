package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"
)

const dateLayout = "2006-01-02"

// Source points to the material used to prepare a knowledge record.
// A provided:// URI identifies a document supplied with the hackathon case.
type Source struct {
	Title        string `json:"title"`
	URI          string `json:"uri"`
	Locator      string `json:"locator"`
	EvidenceKind string `json:"evidence_kind"`
}

// Record is the complete stored entry. Its status and access fields must be
// checked before any part of it is passed to a model.
type Record struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Category      string   `json:"category"`
	ContentKind   string   `json:"content_kind"`
	EvidenceText  string   `json:"evidence_text"`
	Answer        string   `json:"answer"`
	AnswerScope   string   `json:"answer_scope"`
	Source        Source   `json:"source"`
	DataOrigin    string   `json:"data_origin"`
	Status        string   `json:"status"`
	ApprovalScope string   `json:"approval_scope"`
	OwnerID       string   `json:"owner_id"`
	AllowedRoles  []string `json:"allowed_roles"`
	TeamScope     string   `json:"team_scope"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	CheckedAt     string   `json:"checked_at"`
	ExpiresAt     string   `json:"expires_at"`
	TTLDays       int      `json:"ttl_days"`
}

type fileData struct {
	SchemaVersion      string   `json:"schema_version"`
	SnapshotID         string   `json:"snapshot_id"`
	CreatedAt          string   `json:"created_at"`
	Purpose            string   `json:"purpose"`
	ModelContextFields []string `json:"model_context_fields"`
	Records            []Record `json:"records"`
}

// ContextRecord is the deliberately small payload for answer generation.
// It has the same fields listed in knowledge.json's model_context_fields.
type ContextRecord struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	EvidenceText string `json:"evidence_text"`
	Answer       string `json:"answer"`
	AnswerScope  string `json:"answer_scope"`
	Source       Source `json:"source"`
}

type Store struct {
	snapshotID string
	records    []Record
	location   *time.Location
}

func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read knowledge file: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file fileData
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode knowledge file: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("knowledge file contains extra JSON data")
	}
	if file.SchemaVersion != "0.1" || file.SnapshotID == "" || len(file.Records) == 0 {
		return nil, fmt.Errorf("invalid knowledge file header")
	}
	if !slices.Equal(file.ModelContextFields, []string{"id", "title", "evidence_text", "answer", "answer_scope", "source"}) {
		return nil, fmt.Errorf("model context fields do not match loader contract")
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return nil, fmt.Errorf("load knowledge timezone: %w", err)
	}
	seen := make(map[string]bool, len(file.Records))
	for _, record := range file.Records {
		if err := validate(record); err != nil {
			return nil, fmt.Errorf("knowledge record %q: %w", record.ID, err)
		}
		if seen[record.ID] {
			return nil, fmt.Errorf("duplicate knowledge ID %q", record.ID)
		}
		seen[record.ID] = true
	}
	return &Store{snapshotID: file.SnapshotID, records: file.Records, location: location}, nil
}

func (s *Store) SnapshotID() string { return s.snapshotID }

// RecordByID is for presenting metadata after the ID has been checked against
// EligibleContext. It does not grant access on its own.
func (s *Store) RecordByID(id string) (Record, bool) {
	if s == nil {
		return Record{}, false
	}
	for _, record := range s.records {
		if record.ID == id {
			return record, true
		}
	}
	return Record{}, false
}

// ReviewContext includes pending and expired entries only for local duplicate
// suggestions to a content owner. It must never be used as answer context.
func (s *Store) ReviewContext(role, team string) []ContextRecord {
	if s == nil || !validRole(role) {
		return nil
	}
	result := []ContextRecord{}
	for _, record := range s.records {
		if !slices.Contains(record.AllowedRoles, role) || (record.TeamScope != team && record.TeamScope != "all-demo-teams") {
			continue
		}
		result = append(result, ContextRecord{ID: record.ID, Title: record.Title, EvidenceText: record.EvidenceText, Answer: record.Answer})
	}
	return result
}

// EligibleContext filters before retrieval or an external model call. In
// production mode, demo-only and synthetic entries are never returned.
// expires_at is inclusive through the end of that date in Moscow time.
func (s *Store) EligibleContext(role, team string, at time.Time, demo bool) []ContextRecord {
	if s == nil || !validRole(role) || at.IsZero() {
		return nil
	}
	today := at.In(s.location).Format(dateLayout)
	result := make([]ContextRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.Status != "approved" || record.ExpiresAt < today {
			continue
		}
		if !demo && (record.ApprovalScope == "demo_only" || record.DataOrigin == "synthetic") {
			continue
		}
		if !slices.Contains(record.AllowedRoles, role) {
			continue
		}
		if record.TeamScope != "all-demo-teams" && record.TeamScope != team {
			continue
		}
		scope := record.AnswerScope
		if scope == "" {
			scope = "full"
		}
		result = append(result, ContextRecord{
			ID: record.ID, Title: record.Title, EvidenceText: record.EvidenceText,
			Answer: record.Answer, AnswerScope: scope, Source: record.Source,
		})
	}
	return result
}

func validate(r Record) error {
	if r.ID == "" || r.Title == "" || r.EvidenceText == "" || r.Answer == "" || r.OwnerID == "" || r.TeamScope == "" {
		return fmt.Errorf("required field is empty")
	}
	if !slices.Contains([]string{"onboarding", "navigation", "values", "other"}, r.Category) {
		return fmt.Errorf("invalid category")
	}
	if r.ContentKind == "" || !slices.Contains([]string{"case_fact", "public_source", "synthetic", "mentor_verified"}, r.DataOrigin) {
		return fmt.Errorf("invalid content kind or origin")
	}
	if !slices.Contains([]string{"draft", "pending", "approved", "expired"}, r.Status) {
		return fmt.Errorf("invalid status")
	}
	if !slices.Contains([]string{"demo_only", "verified"}, r.ApprovalScope) {
		return fmt.Errorf("invalid approval scope")
	}
	if r.DataOrigin == "synthetic" && r.ApprovalScope != "demo_only" {
		return fmt.Errorf("synthetic knowledge must be demo-only")
	}
	if r.Source.Title == "" || r.Source.Locator == "" || r.Source.EvidenceKind == "" {
		return fmt.Errorf("source is incomplete")
	}
	if r.DataOrigin != "synthetic" && r.Source.URI == "" {
		return fmt.Errorf("non-synthetic knowledge requires a source URI")
	}
	if r.DataOrigin == "public_source" && !strings.HasPrefix(r.Source.URI, "https://") {
		return fmt.Errorf("public source must use HTTPS")
	}
	if len(r.AllowedRoles) == 0 {
		return fmt.Errorf("no allowed roles")
	}
	roles := map[string]bool{}
	for _, role := range r.AllowedRoles {
		if !validRole(role) || roles[role] {
			return fmt.Errorf("invalid or duplicate role")
		}
		roles[role] = true
	}
	for _, value := range []string{r.CreatedAt, r.UpdatedAt} {
		if _, err := time.Parse(dateLayout, value); err != nil {
			return fmt.Errorf("invalid creation or update date")
		}
	}
	if r.TTLDays <= 0 {
		return fmt.Errorf("TTL must be positive")
	}
	if r.Status == "approved" || r.Status == "expired" {
		checked, err := time.Parse(dateLayout, r.CheckedAt)
		if err != nil {
			return fmt.Errorf("invalid checked_at")
		}
		expires, err := time.Parse(dateLayout, r.ExpiresAt)
		if err != nil || !expires.Equal(checked.AddDate(0, 0, r.TTLDays)) {
			return fmt.Errorf("expires_at does not match TTL")
		}
	}
	return nil
}

func validRole(role string) bool {
	return role == "intern" || role == "mentor" || role == "manager"
}
