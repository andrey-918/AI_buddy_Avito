package mockcorp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type AccessRequest struct {
	ID             string `json:"id"`
	ActorID        string `json:"actor_id"`
	ResourceID     string `json:"resource_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"`
	UpdatedAt      string `json:"updated_at"`
}

type Plan struct {
	Status       string   `json:"status"`
	GoalIDs      []string `json:"goal_ids"`
	OKRContextID string   `json:"okr_context_id"`
	UpdatedAt    string   `json:"updated_at"`
}

type Handoff struct {
	ID        string `json:"id"`
	Topic     string `json:"topic"`
	Owner     string `json:"owner"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

type FAQEntry struct {
	ID           string   `json:"id"`
	Question     string   `json:"question"`
	Answer       string   `json:"answer"`
	Status       string   `json:"status"`
	OwnerID      string   `json:"owner_id"`
	DuplicateIDs []string `json:"duplicate_ids"`
	CheckedAt    string   `json:"checked_at,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	UpdatedAt    string   `json:"updated_at"`
}

type AnalyticsEvent struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Role      string `json:"role"`
	Category  string `json:"category,omitempty"`
	Status    string `json:"status,omitempty"`
	SourceID  string `json:"source_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	At        string `json:"at"`
}

type State struct {
	NextID   int              `json:"next_id"`
	Request  *AccessRequest   `json:"request"`
	Plan     Plan             `json:"plan"`
	Done     map[string]bool  `json:"done"`
	Handoffs []Handoff        `json:"handoffs"`
	FAQ      []FAQEntry       `json:"faq"`
	Events   []AnalyticsEvent `json:"events"`
}

var GoalOptions = []struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}{
	{ID: "repository_access", Title: "Получить доступ к учебному репозиторию"},
	{ID: "team_tools", Title: "Освоить рабочие инструменты команды"},
	{ID: "team_context", Title: "Разобраться в зоне ответственности команды"},
}

type Service struct {
	mu     sync.Mutex
	path   string
	secret string
	state  State
}

func New(path, secret string) (*Service, error) {
	s := &Service{path: path, secret: secret, state: State{Plan: Plan{Status: "none"}, Done: map[string]bool{}}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s.state); err != nil {
			return nil, fmt.Errorf("read mock state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.state.Done == nil {
		s.state.Done = map[string]bool{}
	}
	if s.state.Plan.Status == "" {
		s.state.Plan.Status = "none"
	}
	return s, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mock/state", s.getState)
	mux.HandleFunc("POST /mock/access-requests", s.createRequest)
	mux.HandleFunc("POST /mock/access-requests/{id}/decision", s.decideRequest)
	mux.HandleFunc("PUT /mock/plan", s.savePlan)
	mux.HandleFunc("POST /mock/plan/propose", s.proposePlan)
	mux.HandleFunc("POST /mock/plan/respond", s.respondPlan)
	mux.HandleFunc("POST /mock/steps/{id}", s.setStep)
	mux.HandleFunc("POST /mock/handoffs", s.createHandoff)
	mux.HandleFunc("POST /mock/handoffs/{id}/accept", s.acceptHandoff)
	mux.HandleFunc("POST /mock/faq", s.createFAQ)
	mux.HandleFunc("POST /mock/faq/{id}/approve", s.approveFAQ)
	mux.HandleFunc("POST /mock/faq/{id}/revoke", s.revokeFAQ)
	mux.HandleFunc("POST /mock/events", s.recordEvent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mock-Token") != s.secret {
			fail(w, http.StatusUnauthorized, "mock service token required")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) getState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	location, _ := time.LoadLocation("Europe/Moscow")
	today := time.Now().In(location).Format("2006-01-02")
	changed := false
	for index := range s.state.FAQ {
		entry := &s.state.FAQ[index]
		if entry.Status == "approved" && entry.ExpiresAt < today {
			entry.Status = "expired"
			entry.UpdatedAt = now()
			changed = true
		}
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			fail(w, http.StatusInternalServerError, "state could not be saved")
			return
		}
	}
	respond(w, http.StatusOK, s.state)
}

func (s *Service) createRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ActorID        string `json:"actor_id"`
		ResourceID     string `json:"resource_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ActorID != "intern-demo" || in.ResourceID != "backend-repo" || in.IdempotencyKey == "" {
		fail(w, http.StatusBadRequest, "invalid access request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Request != nil {
		respond(w, http.StatusOK, s.state.Request)
		return
	}
	s.state.NextID++
	s.state.Request = &AccessRequest{ID: fmt.Sprintf("REQ-%04d", s.state.NextID), ActorID: in.ActorID, ResourceID: in.ResourceID, IdempotencyKey: in.IdempotencyKey, Status: "waiting_mentor", UpdatedAt: now()}
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusCreated, s.state.Request)
}

func (s *Service) decideRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		fail(w, http.StatusBadRequest, "invalid decision")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Request == nil || s.state.Request.ID != r.PathValue("id") {
		fail(w, http.StatusNotFound, "request not found")
		return
	}
	if s.state.Request.Status != "waiting_mentor" {
		fail(w, http.StatusConflict, "request already decided")
		return
	}
	if in.Decision == "approve" {
		s.state.Request.Status = "approved"
	} else {
		s.state.Request.Status = "rejected"
	}
	s.state.Request.UpdatedAt = now()
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusOK, s.state.Request)
}

func (s *Service) savePlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GoalIDs []string `json:"goal_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.GoalIDs) < 1 || len(in.GoalIDs) > 3 {
		fail(w, http.StatusBadRequest, "select one to three goals")
		return
	}
	seen := map[string]bool{}
	for _, id := range in.GoalIDs {
		valid := false
		for _, option := range GoalOptions {
			if option.ID == id {
				valid = true
				break
			}
		}
		if !valid || seen[id] {
			fail(w, http.StatusBadRequest, "invalid or duplicate goal")
			return
		}
		seen[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Plan = Plan{Status: "draft", GoalIDs: slices.Clone(in.GoalIDs), OKRContextID: "synthetic-team-okr", UpdatedAt: now()}
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusOK, s.state.Plan)
}

func (s *Service) proposePlan(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Plan.Status != "draft" && s.state.Plan.Status != "changes_requested" {
		fail(w, http.StatusConflict, "plan is not a draft")
		return
	}
	s.state.Plan.Status = "awaiting_intern"
	s.state.Plan.UpdatedAt = now()
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusOK, s.state.Plan)
}

func (s *Service) respondPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Decision != "agree" && in.Decision != "request_changes" {
		fail(w, http.StatusBadRequest, "invalid decision")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Plan.Status != "awaiting_intern" {
		fail(w, http.StatusConflict, "plan is not awaiting intern")
		return
	}
	if in.Decision == "agree" {
		s.state.Plan.Status = "agreed"
	} else {
		s.state.Plan.Status = "changes_requested"
	}
	s.state.Plan.UpdatedAt = now()
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusOK, s.state.Plan)
}

func (s *Service) setStep(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 3 || id[1] != '-' || id[0] < '0' || id[0] > '4' || id[2] < '0' || id[2] > '1' {
		fail(w, http.StatusBadRequest, "invalid step")
		return
	}
	var in struct {
		Done bool `json:"done"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Done {
		s.state.Done[id] = true
	} else {
		delete(s.state.Done, id)
	}
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusOK, struct {
		Done map[string]bool `json:"done"`
	}{Done: s.state.Done})
}

func (s *Service) createHandoff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Topic string `json:"topic"`
		Owner string `json:"owner"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !slices.Contains([]string{"merge", "staging", "unknown"}, in.Topic) ||
		!slices.Contains([]string{"Наставник команды", "Platform Tools"}, in.Owner) {
		fail(w, http.StatusBadRequest, "invalid handoff")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, handoff := range s.state.Handoffs {
		if handoff.Topic == in.Topic && handoff.Owner == in.Owner && handoff.Status == "pending" {
			respond(w, http.StatusOK, handoff)
			return
		}
	}
	s.state.NextID++
	handoff := Handoff{ID: fmt.Sprintf("HG-%04d", s.state.NextID), Topic: in.Topic, Owner: in.Owner, Status: "pending", UpdatedAt: now()}
	s.state.Handoffs = append(s.state.Handoffs, handoff)
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusCreated, handoff)
}

func (s *Service) acceptHandoff(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.state.Handoffs {
		handoff := &s.state.Handoffs[index]
		if handoff.ID != r.PathValue("id") {
			continue
		}
		if handoff.Status != "pending" {
			fail(w, http.StatusConflict, "handoff already accepted")
			return
		}
		handoff.Status = "accepted"
		handoff.UpdatedAt = now()
		if err := s.saveLocked(); err != nil {
			fail(w, http.StatusInternalServerError, "state could not be saved")
			return
		}
		respond(w, http.StatusOK, handoff)
		return
	}
	fail(w, http.StatusNotFound, "handoff not found")
}

func (s *Service) createFAQ(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Question     string   `json:"question"`
		Answer       string   `json:"answer"`
		DuplicateIDs []string `json:"duplicate_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Question) == "" || strings.TrimSpace(in.Answer) == "" || len(in.Question) > 1000 || len(in.Answer) > 2000 {
		fail(w, http.StatusBadRequest, "invalid FAQ content")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.NextID++
	entry := FAQEntry{ID: fmt.Sprintf("FAQ-%04d", s.state.NextID), Question: strings.TrimSpace(in.Question), Answer: strings.TrimSpace(in.Answer), Status: "pending", OwnerID: "mentor-backend-demo", DuplicateIDs: in.DuplicateIDs, UpdatedAt: now()}
	s.state.FAQ = append(s.state.FAQ, entry)
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusCreated, entry)
}

func (s *Service) approveFAQ(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.state.FAQ {
		entry := &s.state.FAQ[index]
		if entry.ID != r.PathValue("id") {
			continue
		}
		if entry.Status != "pending" && entry.Status != "expired" && entry.Status != "approved" {
			fail(w, http.StatusConflict, "FAQ cannot be approved")
			return
		}
		location, _ := time.LoadLocation("Europe/Moscow")
		today := time.Now().In(location)
		entry.Status = "approved"
		entry.CheckedAt = today.Format("2006-01-02")
		entry.ExpiresAt = today.AddDate(0, 0, 60).Format("2006-01-02")
		entry.UpdatedAt = now()
		if err := s.saveLocked(); err != nil {
			fail(w, http.StatusInternalServerError, "state could not be saved")
			return
		}
		respond(w, http.StatusOK, entry)
		return
	}
	fail(w, http.StatusNotFound, "FAQ not found")
}

func (s *Service) revokeFAQ(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.state.FAQ {
		entry := &s.state.FAQ[index]
		if entry.ID != r.PathValue("id") {
			continue
		}
		if entry.Status != "approved" {
			fail(w, http.StatusConflict, "FAQ is not approved")
			return
		}
		entry.Status = "expired"
		entry.UpdatedAt = now()
		if err := s.saveLocked(); err != nil {
			fail(w, http.StatusInternalServerError, "state could not be saved")
			return
		}
		respond(w, http.StatusOK, entry)
		return
	}
	fail(w, http.StatusNotFound, "FAQ not found")
}

func (s *Service) recordEvent(w http.ResponseWriter, r *http.Request) {
	var in AnalyticsEvent
	if !decode(w, r, &in) {
		return
	}
	validTypes := []string{"session_started", "request_created", "intent_detected", "knowledge_search", "answer_shown", "user_feedback", "human_gate_triggered", "human_handoff_completed", "fallback_triggered", "action_completed"}
	validRole := slices.Contains([]string{"intern", "mentor", "manager"}, in.Role)
	if !slices.Contains(validTypes, in.Type) || !validRole || len(in.RequestID) > 64 || len(in.Category) > 40 || len(in.Status) > 40 || len(in.SourceID) > 40 || len(in.Reason) > 60 ||
		strings.IndexFunc(in.RequestID, func(char rune) bool {
			return !(char == '-' || char == '_' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z')
		}) >= 0 {
		fail(w, http.StatusBadRequest, "invalid analytics event")
		return
	}
	in.At = now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Events = append(s.state.Events, in)
	if len(s.state.Events) > 500 {
		s.state.Events = s.state.Events[len(s.state.Events)-500:]
	}
	if err := s.saveLocked(); err != nil {
		fail(w, http.StatusInternalServerError, "state could not be saved")
		return
	}
	respond(w, http.StatusCreated, map[string]string{"status": "recorded"})
}

func (s *Service) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, http.StatusBadRequest, "unexpected JSON data")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": strings.TrimSpace(message)})
}
