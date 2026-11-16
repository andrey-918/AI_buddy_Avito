package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-buddy-avito/internal/knowledge"
	"ai-buddy-avito/internal/mockcorp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testApp(t *testing.T) (*app, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	service, err := mockcorp.New(path, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{sessions: map[string]session{}, mockURL: "http://mock.local", mockToken: "test-secret", staticDir: "../../dist"}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		service.Handler().ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})}
	return a, path
}

func call(t *testing.T, a *app, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(data)
	}
	var rawBody string
	if payload != nil {
		data, err := io.ReadAll(payload)
		if err != nil {
			t.Fatal(err)
		}
		rawBody = string(data)
	}
	return callRaw(t, a, method, path, token, rawBody)
}

func callRaw(t *testing.T, a *app, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	a.handler().ServeHTTP(recorder, req)
	var result map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s %s: invalid JSON: %v", method, path, err)
	}
	return recorder.Code, result
}

func login(t *testing.T, a *app, role string) string {
	t.Helper()
	status, body := call(t, a, http.MethodPost, "/api/demo/session", "", map[string]any{"role": role})
	if status != http.StatusOK {
		t.Fatalf("login %s: %d %v", role, status, body)
	}
	return body["token"].(string)
}

func requireStatus(t *testing.T, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("status %d, want %d: %v", got, want, body)
	}
}

func TestAccessRequestUsesMockAPIAndRoleChecks(t *testing.T) {
	a, _ := testApp(t)
	intern, mentor, manager := login(t, a, "intern"), login(t, a, "mentor"), login(t, a, "manager")
	status, body := call(t, a, http.MethodPost, "/api/access-requests", manager, nil)
	requireStatus(t, status, http.StatusForbidden, body)
	status, body = call(t, a, http.MethodPost, "/api/access-requests", intern, nil)
	requireStatus(t, status, http.StatusCreated, body)
	id := body["id"].(string)
	if body["status"] != "waiting_mentor" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/access-requests", intern, nil)
	requireStatus(t, status, http.StatusOK, body)
	if body["id"] != id {
		t.Fatalf("duplicate request created: %v", body)
	}
	status, body = call(t, a, http.MethodPost, "/api/access-requests/"+id+"/decision", intern, map[string]any{"decision": "approve"})
	requireStatus(t, status, http.StatusForbidden, body)
	status, body = call(t, a, http.MethodPost, "/api/access-requests/"+id+"/decision", mentor, map[string]any{"decision": "approve"})
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "approved" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodGet, "/api/bootstrap", intern, nil)
	requireStatus(t, status, http.StatusOK, body)
	request := body["request"].(map[string]any)
	if request["status"] != "approved" {
		t.Fatal(body)
	}
}

func TestPlanAgreementAndStepPersistThroughMockAPI(t *testing.T) {
	a, path := testApp(t)
	intern, mentor, manager := login(t, a, "intern"), login(t, a, "mentor"), login(t, a, "manager")
	status, body := call(t, a, http.MethodPut, "/api/plan", mentor, map[string]any{"goal_ids": []string{"team_tools"}})
	requireStatus(t, status, http.StatusForbidden, body)
	status, body = call(t, a, http.MethodPut, "/api/plan", manager, map[string]any{"goal_ids": []string{"repository_access", "team_context"}})
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "draft" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/plan/propose", manager, nil)
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "awaiting_intern" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/plan/respond", intern, map[string]any{"decision": "request_changes"})
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "changes_requested" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPut, "/api/plan", manager, map[string]any{"goal_ids": []string{"repository_access", "team_tools"}})
	requireStatus(t, status, http.StatusOK, body)
	status, body = call(t, a, http.MethodPost, "/api/plan/propose", manager, nil)
	requireStatus(t, status, http.StatusOK, body)
	status, body = call(t, a, http.MethodPost, "/api/plan/respond", intern, map[string]any{"decision": "agree"})
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "agreed" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/steps/0-0", intern, map[string]any{"done": true})
	requireStatus(t, status, http.StatusOK, body)
	status, body = call(t, a, http.MethodGet, "/api/bootstrap", manager, nil)
	requireStatus(t, status, http.StatusOK, body)
	if body["plan"].(map[string]any)["status"] != "agreed" || len(body["done"].([]any)) != 1 {
		t.Fatal(body)
	}
	reloaded, err := mockcorp.New(path, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/mock/state", nil)
	request.Header.Set("X-Mock-Token", "test-secret")
	recorder := httptest.NewRecorder()
	reloaded.Handler().ServeHTTP(recorder, request)
	var saved mockcorp.State
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Plan.Status != "agreed" || !saved.Done["0-0"] {
		t.Fatalf("state not persisted: %+v", saved)
	}
}

func TestChatUsesEligibleKnowledgeAndGuardrail(t *testing.T) {
	a, _ := testApp(t)
	store, err := knowledge.Load("../../data/knowledge.json")
	if err != nil {
		t.Fatal(err)
	}
	a.knowledge = store
	t.Setenv("BUDDY_ML_SCRIPT", "../../ml/runtime.py")
	t.Setenv("BUDDY_LLM_URL", "")
	t.Setenv("BUDDY_LLM_MODEL", "")
	t.Setenv("BUDDY_LLM_API_KEY", "")
	intern := login(t, a, "intern")
	status, body := call(t, a, http.MethodPost, "/api/chat", intern, map[string]string{"question": "Как получить доступ к учебному репозиторию?"})
	requireStatus(t, status, http.StatusOK, body)
	if body["outcome"] != "answer" || body["source"].(map[string]any)["id"] != "K-101" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/chat", intern, map[string]string{"question": "Как устроен staging?"})
	requireStatus(t, status, http.StatusOK, body)
	if body["outcome"] != "human_gate" {
		t.Fatal(body)
	}
	t.Setenv("BUDDY_LLM_URL", "https://example.org/v1/chat/completions")
	t.Setenv("BUDDY_LLM_MODEL", "fake-model")
	t.Setenv("BUDDY_LLM_API_KEY", "fake-key")
	status, body = call(t, a, http.MethodPost, "/api/chat", intern, map[string]string{"question": "Мой паспорт: 1234 567890. Что делать?"})
	requireStatus(t, status, http.StatusOK, body)
	if body["outcome"] != "fallback" || body["reason"] != "sensitive_data" {
		t.Fatal(body)
	}
}

func TestKnowledgeLoopAndHandoffPersist(t *testing.T) {
	a, path := testApp(t)
	store, err := knowledge.Load("../../data/knowledge.json")
	if err != nil {
		t.Fatal(err)
	}
	a.knowledge = store
	t.Setenv("BUDDY_DUP_SCRIPT", "../../ml/duplicates.py")
	t.Setenv("BUDDY_ML_SCRIPT", "../../ml/runtime.py")
	t.Setenv("BUDDY_LLM_URL", "")
	t.Setenv("BUDDY_LLM_MODEL", "")
	t.Setenv("BUDDY_LLM_API_KEY", "")
	intern, mentor := login(t, a, "intern"), login(t, a, "mentor")
	status, body := call(t, a, http.MethodPost, "/api/handoffs", intern, map[string]string{"topic": "merge"})
	requireStatus(t, status, http.StatusCreated, body)
	handoffID := body["id"].(string)
	status, body = call(t, a, http.MethodPost, "/api/handoffs/"+handoffID+"/accept", intern, nil)
	requireStatus(t, status, http.StatusForbidden, body)
	status, body = call(t, a, http.MethodPost, "/api/handoffs/"+handoffID+"/accept", mentor, nil)
	requireStatus(t, status, http.StatusOK, body)
	status, body = call(t, a, http.MethodPost, "/api/faq", mentor, map[string]string{"question": "Кто выдаёт доступ к учебному репозиторию?", "answer": "Для учебного репозитория стажёр создаёт заявку, а наставник её рассматривает."})
	requireStatus(t, status, http.StatusCreated, body)
	if body["status"] != "pending" || len(body["duplicate_ids"].([]any)) == 0 {
		t.Fatal(body)
	}
	faqID := body["id"].(string)
	status, body = call(t, a, http.MethodPost, "/api/faq/"+faqID+"/approve", intern, nil)
	requireStatus(t, status, http.StatusForbidden, body)
	status, body = call(t, a, http.MethodPost, "/api/faq/"+faqID+"/approve", mentor, nil)
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "approved" {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/chat", intern, map[string]string{"question": "Кто выдаёт доступ к учебному репозиторию?"})
	requireStatus(t, status, http.StatusOK, body)
	if body["outcome"] != "answer" || body["source"].(map[string]any)["id"] != faqID {
		t.Fatal(body)
	}
	status, body = call(t, a, http.MethodPost, "/api/faq/"+faqID+"/revoke", mentor, nil)
	requireStatus(t, status, http.StatusOK, body)
	if body["status"] != "expired" {
		t.Fatal(body)
	}
	reloaded, err := mockcorp.New(path, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/mock/state", nil)
	request.Header.Set("X-Mock-Token", "test-secret")
	recorder := httptest.NewRecorder()
	reloaded.Handler().ServeHTTP(recorder, request)
	var saved mockcorp.State
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Handoffs) != 1 || saved.Handoffs[0].Status != "accepted" || len(saved.FAQ) != 1 || saved.FAQ[0].Status != "expired" {
		t.Fatalf("state not persisted: %+v", saved)
	}
}

func TestAnalyticsStoresOnlyAllowedFields(t *testing.T) {
	a, path := testApp(t)
	intern, manager := login(t, a, "intern"), login(t, a, "manager")
	status, body := call(t, a, http.MethodPost, "/api/events", intern, map[string]any{"type": "request_created", "request_id": "demo-123", "category": "onboarding", "status": "", "source_id": "", "reason": ""})
	requireStatus(t, status, http.StatusCreated, body)
	status, body = call(t, a, http.MethodPost, "/api/events", intern, map[string]any{"type": "answer_shown", "request_id": "demo-123", "category": "onboarding", "status": "", "source_id": "K-101", "reason": ""})
	requireStatus(t, status, http.StatusCreated, body)
	status, body = call(t, a, http.MethodPost, "/api/events", intern, map[string]any{"type": "request_created", "question": "secret text"})
	requireStatus(t, status, http.StatusBadRequest, body)
	status, body = call(t, a, http.MethodGet, "/api/bootstrap", manager, nil)
	requireStatus(t, status, http.StatusOK, body)
	metrics := body["metrics"].(map[string]any)
	if metrics["requests"] != float64(1) || metrics["answers"] != float64(1) {
		t.Fatal(metrics)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret text")) {
		t.Fatal("raw question was persisted")
	}
}

func TestAnalyticsSummaryCountsEachOutcome(t *testing.T) {
	events := []mockcorp.AnalyticsEvent{
		{Type: "request_created"},
		{Type: "answer_shown"},
		{Type: "human_gate_triggered"},
		{Type: "human_handoff_completed"},
		{Type: "fallback_triggered"},
		{Type: "user_feedback", Status: "helpful"},
		{Type: "user_feedback", Status: "not_helpful"},
		{Type: "session_started"},
	}
	got := analyticsSummary(events)
	want := map[string]int{
		"requests": 1, "answers": 1, "handoffs": 1,
		"accepted": 1, "helpful": 1, "fallbacks": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %d, want %d", key, got[key], value)
		}
	}
}

func TestSessionInputAndAuthorizationValidation(t *testing.T) {
	a, _ := testApp(t)
	status, body := callRaw(t, a, http.MethodPost, "/api/demo/session", "", `{`)
	requireStatus(t, status, http.StatusBadRequest, body)
	status, body = call(t, a, http.MethodPost, "/api/demo/session", "", map[string]string{"role": "guest"})
	requireStatus(t, status, http.StatusBadRequest, body)
	status, body = callRaw(t, a, http.MethodPost, "/api/demo/session", "", `{"role":"intern"} {}`)
	requireStatus(t, status, http.StatusBadRequest, body)

	status, body = callRaw(t, a, http.MethodGet, "/api/bootstrap", "", "")
	requireStatus(t, status, http.StatusUnauthorized, body)
}
