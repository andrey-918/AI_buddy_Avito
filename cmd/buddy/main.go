package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-buddy-avito/internal/knowledge"
	"ai-buddy-avito/internal/mockcorp"
)

type session struct {
	Role    string
	ActorID string
}

type app struct {
	mu        sync.RWMutex
	sessions  map[string]session
	mockURL   string
	mockToken string
	client    *http.Client
	staticDir string
	knowledge *knowledge.Store
}

func main() {
	dataFile := env("BUDDY_DATA_FILE", "data/mock-state.json")
	staticDir := env("BUDDY_STATIC_DIR", "dist")
	knowledgeStore, err := knowledge.Load(env("BUDDY_KNOWLEDGE_FILE", "data/knowledge.json"))
	if err != nil {
		log.Fatal(err)
	}
	port := env("PORT", "4173")
	secret, err := randomToken()
	if err != nil {
		log.Fatal(err)
	}
	mock, err := mockcorp.New(dataFile, secret)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	mockServer := &http.Server{Handler: mock.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := mockServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
	}()
	defer mockServer.Shutdown(context.Background())
	a := &app{sessions: map[string]session{}, mockURL: "http://" + listener.Addr().String(), mockToken: secret, client: &http.Client{Timeout: 3 * time.Second}, staticDir: staticDir, knowledge: knowledgeStore}
	log.Printf("AI-buddy at http://127.0.0.1:%s", port)
	server := &http.Server{Addr: ":" + port, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/demo/session", a.createSession)
	mux.HandleFunc("POST /api/chat", a.authorize(a.chatReply))
	mux.HandleFunc("POST /api/events", a.authorize(a.recordEvent))
	mux.HandleFunc("POST /api/handoffs", a.authorize(a.createHandoff))
	mux.HandleFunc("POST /api/handoffs/{id}/accept", a.authorize(a.acceptHandoff))
	mux.HandleFunc("POST /api/faq", a.authorize(a.createFAQ))
	mux.HandleFunc("POST /api/faq/{id}/approve", a.authorize(a.approveFAQ))
	mux.HandleFunc("POST /api/faq/{id}/revoke", a.authorize(a.revokeFAQ))
	mux.HandleFunc("GET /api/bootstrap", a.authorize(a.bootstrap))
	mux.HandleFunc("POST /api/access-requests", a.authorize(a.createAccessRequest))
	mux.HandleFunc("POST /api/access-requests/{id}/decision", a.authorize(a.decideAccessRequest))
	mux.HandleFunc("PUT /api/plan", a.authorize(a.savePlan))
	mux.HandleFunc("POST /api/plan/propose", a.authorize(a.proposePlan))
	mux.HandleFunc("POST /api/plan/respond", a.authorize(a.respondPlan))
	mux.HandleFunc("POST /api/steps/{id}", a.authorize(a.setStep))
	mux.Handle("/", http.FileServer(http.Dir(a.staticDir)))
	return mux
}

func (a *app) createSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	actor := map[string]string{"intern": "intern-demo", "mentor": "mentor-demo", "manager": "manager-demo"}[in.Role]
	if actor == "" {
		apiFail(w, http.StatusBadRequest, "unknown role")
		return
	}
	token, err := randomToken()
	if err != nil {
		apiFail(w, http.StatusInternalServerError, "session unavailable")
		return
	}
	a.mu.Lock()
	a.sessions[token] = session{Role: in.Role, ActorID: actor}
	a.mu.Unlock()
	jsonReply(w, http.StatusOK, map[string]string{"token": token, "role": in.Role, "actor_id": actor})
}

func (a *app) authorize(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		a.mu.RLock()
		actor, ok := a.sessions[token]
		a.mu.RUnlock()
		if !ok || token == "" {
			apiFail(w, http.StatusUnauthorized, "demo session required")
			return
		}
		next(w, r, actor)
	}
}

func (a *app) bootstrap(w http.ResponseWriter, r *http.Request, actor session) {
	var state mockcorp.State
	status, err := a.mock(r.Context(), http.MethodGet, "/mock/state", nil, &state)
	if err != nil {
		apiFail(w, http.StatusBadGateway, "mock service unavailable")
		return
	}
	if status != http.StatusOK {
		apiFail(w, status, "mock service error")
		return
	}
	done := []string{}
	if actor.Role != "mentor" {
		for id, checked := range state.Done {
			if checked {
				done = append(done, id)
			}
		}
		sort.Strings(done)
	}
	var plan any
	if actor.Role == "intern" || actor.Role == "manager" {
		plan = state.Plan
	}
	handoffs := []mockcorp.Handoff{}
	for _, handoff := range state.Handoffs {
		if actor.Role == "mentor" && handoff.Owner != "Наставник команды" {
			continue
		}
		handoffs = append(handoffs, handoff)
	}
	faq := []mockcorp.FAQEntry{}
	if actor.Role == "mentor" {
		faq = state.FAQ
	}
	metrics := map[string]int{}
	if actor.Role == "manager" {
		metrics = analyticsSummary(state.Events)
	}
	jsonReply(w, http.StatusOK, map[string]any{"role": actor.Role, "request": state.Request, "plan": plan, "done": done, "goal_options": mockcorp.GoalOptions, "handoffs": handoffs, "faq": faq, "metrics": metrics})
}

func (a *app) createAccessRequest(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "intern") {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/access-requests", map[string]string{"actor_id": actor.ActorID, "resource_id": "backend-repo", "idempotency_key": actor.ActorID + ":backend-repo"})
}

func (a *app) decideAccessRequest(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "mentor") {
		return
	}
	var in struct {
		Decision string `json:"decision"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/access-requests/"+r.PathValue("id")+"/decision", in)
}

func (a *app) savePlan(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "manager") {
		return
	}
	var in struct {
		GoalIDs []string `json:"goal_ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a.forward(w, r, http.MethodPut, "/mock/plan", in)
}

func (a *app) proposePlan(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "manager") {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/plan/propose", nil)
}

func (a *app) respondPlan(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "intern") {
		return
	}
	var in struct {
		Decision string `json:"decision"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/plan/respond", in)
}

func (a *app) setStep(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "intern") {
		return
	}
	var in struct {
		Done bool `json:"done"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/steps/"+r.PathValue("id"), in)
}

func requireRole(w http.ResponseWriter, actor session, role string) bool {
	if actor.Role != role {
		apiFail(w, http.StatusForbidden, "role cannot perform this action")
		return false
	}
	return true
}

func (a *app) forward(w http.ResponseWriter, r *http.Request, method, path string, input any) {
	var output json.RawMessage
	status, err := a.mock(r.Context(), method, path, input, &output)
	if err != nil {
		apiFail(w, http.StatusBadGateway, "mock service unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(output)
}

func (a *app) mock(ctx context.Context, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.mockURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Mock-Token", a.mockToken)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, err
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			return 0, fmt.Errorf("mock response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		apiFail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		apiFail(w, http.StatusBadRequest, "unexpected JSON data")
		return false
	}
	return true
}

func jsonReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiFail(w http.ResponseWriter, status int, message string) {
	jsonReply(w, status, map[string]string{"error": message})
}
