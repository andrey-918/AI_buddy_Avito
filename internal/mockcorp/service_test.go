package mockcorp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testService(t *testing.T) *Service {
	t.Helper()
	service, err := New(filepath.Join(t.TempDir(), "state.json"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func callService(t *testing.T, service *Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Mock-Token", "test-secret")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	return recorder
}

func statusIs(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, want, recorder.Body.String())
	}
}

func blockPersistence(t *testing.T, service *Service) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	service.path = filepath.Join(parent, "state.json")
}

func TestNewLoadsAndNormalizesState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	service, err := New(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if service.state.Plan.Status != "none" || service.state.Done == nil {
		t.Fatalf("unexpected initial state: %+v", service.state)
	}

	if err := os.WriteFile(path, []byte(`{"next_id":2,"done":null,"plan":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.state.NextID != 2 || loaded.state.Plan.Status != "none" || loaded.state.Done == nil {
		t.Fatalf("loaded state was not normalized: %+v", loaded.state)
	}

	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path, "secret"); err == nil {
		t.Fatal("malformed persisted state was accepted")
	}

	if _, err := New(t.TempDir(), "secret"); err == nil {
		t.Fatal("reading a directory as state was accepted")
	}
}

func TestHandlerRequiresMockToken(t *testing.T) {
	service := testService(t)
	request := httptest.NewRequest(http.MethodGet, "/mock/state", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	statusIs(t, recorder, http.StatusUnauthorized)
	if !strings.Contains(recorder.Body.String(), "mock service token required") {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}

	recorder = callService(t, service, http.MethodGet, "/mock/state", "")
	statusIs(t, recorder, http.StatusOK)
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content type = %q", got)
	}
}

func TestAccessRequestLifecycleAndValidation(t *testing.T) {
	service := testService(t)
	for _, body := range []string{
		`{`,
		`{"actor_id":"wrong","resource_id":"backend-repo","idempotency_key":"x"}`,
		`{"actor_id":"intern-demo","resource_id":"other","idempotency_key":"x"}`,
		`{"actor_id":"intern-demo","resource_id":"backend-repo"}`,
	} {
		statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests", body), http.StatusBadRequest)
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests", `{} {}`), http.StatusBadRequest)

	created := callService(t, service, http.MethodPost, "/mock/access-requests",
		`{"actor_id":"intern-demo","resource_id":"backend-repo","idempotency_key":"intern-demo:repo"}`)
	statusIs(t, created, http.StatusCreated)
	var request AccessRequest
	if err := json.Unmarshal(created.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if request.ID != "REQ-0001" || request.Status != "waiting_mentor" {
		t.Fatalf("unexpected request: %+v", request)
	}
	duplicate := callService(t, service, http.MethodPost, "/mock/access-requests",
		`{"actor_id":"intern-demo","resource_id":"backend-repo","idempotency_key":"another-key"}`)
	statusIs(t, duplicate, http.StatusOK)
	var duplicateRequest AccessRequest
	if err := json.Unmarshal(duplicate.Body.Bytes(), &duplicateRequest); err != nil {
		t.Fatal(err)
	}
	if duplicateRequest.ID != request.ID {
		t.Fatalf("duplicate request changed ID: %s", duplicateRequest.ID)
	}

	statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests/missing/decision", `{"decision":"approve"}`), http.StatusNotFound)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests/"+request.ID+"/decision", `{}`), http.StatusBadRequest)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests/"+request.ID+"/decision", `{"decision":"maybe"}`), http.StatusBadRequest)
	approved := callService(t, service, http.MethodPost, "/mock/access-requests/"+request.ID+"/decision", `{"decision":"approve"}`)
	statusIs(t, approved, http.StatusOK)
	if !strings.Contains(approved.Body.String(), `"status":"approved"`) {
		t.Fatalf("request was not approved: %s", approved.Body.String())
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests/"+request.ID+"/decision", `{"decision":"reject"}`), http.StatusConflict)

	rejectService := testService(t)
	callService(t, rejectService, http.MethodPost, "/mock/access-requests",
		`{"actor_id":"intern-demo","resource_id":"backend-repo","idempotency_key":"key"}`)
	statusIs(t, callService(t, rejectService, http.MethodPost, "/mock/access-requests/REQ-0001/decision", `{"decision":"reject"}`), http.StatusOK)
}

func TestAccessRequestPersistenceFailures(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		service := testService(t)
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests",
			`{"actor_id":"intern-demo","resource_id":"backend-repo","idempotency_key":"key"}`), http.StatusInternalServerError)
	})
	t.Run("decision", func(t *testing.T) {
		service := testService(t)
		service.state.Request = &AccessRequest{ID: "REQ-0001", Status: "waiting_mentor"}
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/access-requests/REQ-0001/decision", `{"decision":"approve"}`), http.StatusInternalServerError)
	})
}

func TestPlanLifecycleAndValidation(t *testing.T) {
	service := testService(t)
	for _, body := range []string{
		`{`,
		`{"goal_ids":[]}`,
		`{"goal_ids":["repository_access","team_tools","team_context","repository_access"]}`,
		`{"goal_ids":["unknown"]}`,
		`{"goal_ids":["repository_access","repository_access"]}`,
	} {
		statusIs(t, callService(t, service, http.MethodPut, "/mock/plan", body), http.StatusBadRequest)
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/propose", ""), http.StatusConflict)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/respond", `{"decision":"agree"}`), http.StatusConflict)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/respond", `{"decision":"invalid"}`), http.StatusBadRequest)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/respond", `{`), http.StatusBadRequest)

	saved := callService(t, service, http.MethodPut, "/mock/plan", `{"goal_ids":["repository_access","team_tools"]}`)
	statusIs(t, saved, http.StatusOK)
	if !strings.Contains(saved.Body.String(), `"status":"draft"`) {
		t.Fatalf("unexpected plan: %s", saved.Body.String())
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/propose", ""), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/respond", `{"decision":"request_changes"}`), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/propose", ""), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/respond", `{"decision":"agree"}`), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/propose", ""), http.StatusConflict)
}

func TestPlanPersistenceFailures(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		service := testService(t)
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPut, "/mock/plan", `{"goal_ids":["team_tools"]}`), http.StatusInternalServerError)
	})
	t.Run("propose", func(t *testing.T) {
		service := testService(t)
		service.state.Plan.Status = "draft"
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/propose", ""), http.StatusInternalServerError)
	})
	t.Run("respond", func(t *testing.T) {
		service := testService(t)
		service.state.Plan.Status = "awaiting_intern"
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/plan/respond", `{"decision":"agree"}`), http.StatusInternalServerError)
	})
}

func TestStepsValidateAndPersist(t *testing.T) {
	service := testService(t)
	for _, id := range []string{"0-2", "5-0", "0-0-extra"} {
		statusIs(t, callService(t, service, http.MethodPost, "/mock/steps/"+id, `{"done":true}`), http.StatusBadRequest)
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/steps/0-0", `{`), http.StatusBadRequest)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/steps/0-0", `{"done":true}`), http.StatusOK)
	if !service.state.Done["0-0"] {
		t.Fatal("step was not marked done")
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/steps/0-0", `{"done":false}`), http.StatusOK)
	if service.state.Done["0-0"] {
		t.Fatal("step was not cleared")
	}
	blockPersistence(t, service)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/steps/0-1", `{"done":true}`), http.StatusInternalServerError)
}

func TestHandoffLifecycleDeduplicatesPendingItems(t *testing.T) {
	service := testService(t)
	for _, body := range []string{
		`{`,
		`{"topic":"other","owner":"Наставник команды"}`,
		`{"topic":"merge","owner":"unknown"}`,
	} {
		statusIs(t, callService(t, service, http.MethodPost, "/mock/handoffs", body), http.StatusBadRequest)
	}

	created := callService(t, service, http.MethodPost, "/mock/handoffs", `{"topic":"merge","owner":"Наставник команды"}`)
	statusIs(t, created, http.StatusCreated)
	var handoff Handoff
	if err := json.Unmarshal(created.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	duplicate := callService(t, service, http.MethodPost, "/mock/handoffs", `{"topic":"merge","owner":"Наставник команды"}`)
	statusIs(t, duplicate, http.StatusOK)
	if !strings.Contains(duplicate.Body.String(), handoff.ID) || len(service.state.Handoffs) != 1 {
		t.Fatalf("pending handoff was not deduplicated: %s", duplicate.Body.String())
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/handoffs/missing/accept", ""), http.StatusNotFound)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/handoffs/"+handoff.ID+"/accept", ""), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/handoffs/"+handoff.ID+"/accept", ""), http.StatusConflict)

	createdAgain := callService(t, service, http.MethodPost, "/mock/handoffs", `{"topic":"merge","owner":"Наставник команды"}`)
	statusIs(t, createdAgain, http.StatusCreated)
	if len(service.state.Handoffs) != 2 {
		t.Fatalf("accepted handoff prevented a new transfer: %+v", service.state.Handoffs)
	}
}

func TestHandoffPersistenceFailures(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		service := testService(t)
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/handoffs", `{"topic":"unknown","owner":"Наставник команды"}`), http.StatusInternalServerError)
	})
	t.Run("accept", func(t *testing.T) {
		service := testService(t)
		service.state.Handoffs = []Handoff{{ID: "HG-0001", Status: "pending"}}
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/handoffs/HG-0001/accept", ""), http.StatusInternalServerError)
	})
}

func TestFAQLifecycleAndExpiry(t *testing.T) {
	service := testService(t)
	for _, body := range []string{
		`{`,
		`{"question":" ","answer":"answer"}`,
		`{"question":"question","answer":" "}`,
		`{"question":"` + strings.Repeat("q", 1001) + `","answer":"answer"}`,
		`{"question":"question","answer":"` + strings.Repeat("a", 2001) + `"}`,
	} {
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq", body), http.StatusBadRequest)
	}
	created := callService(t, service, http.MethodPost, "/mock/faq", `{"question":"  Where is the repo?  ","answer":"  Ask your mentor.  ","duplicate_ids":["K-101"]}`)
	statusIs(t, created, http.StatusCreated)
	var faq FAQEntry
	if err := json.Unmarshal(created.Body.Bytes(), &faq); err != nil {
		t.Fatal(err)
	}
	if faq.Question != "Where is the repo?" || faq.Answer != "Ask your mentor." || faq.Status != "pending" || faq.OwnerID == "" {
		t.Fatalf("FAQ content was not normalized: %+v", faq)
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/missing/approve", ""), http.StatusNotFound)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/"+faq.ID+"/revoke", ""), http.StatusConflict)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/"+faq.ID+"/approve", ""), http.StatusOK)
	if !service.state.FAQ[0].CheckedAtIsCurrent() {
		t.Fatal("approval did not assign a Moscow date and expiry")
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/"+faq.ID+"/approve", ""), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/"+faq.ID+"/revoke", ""), http.StatusOK)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/"+faq.ID+"/approve", ""), http.StatusOK)

	service.state.FAQ = append(service.state.FAQ, FAQEntry{ID: "FAQ-expired", Status: "approved", ExpiresAt: "2000-01-01"})
	statusIs(t, callService(t, service, http.MethodGet, "/mock/state", ""), http.StatusOK)
	if service.state.FAQ[1].Status != "expired" {
		t.Fatalf("expired FAQ status not updated: %+v", service.state.FAQ[1])
	}
}

func (entry FAQEntry) CheckedAtIsCurrent() bool {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return false
	}
	today := time.Now().In(location)
	checked, err := time.Parse("2006-01-02", entry.CheckedAt)
	if err != nil || checked.Format("2006-01-02") != today.Format("2006-01-02") {
		return false
	}
	expires, err := time.Parse("2006-01-02", entry.ExpiresAt)
	return err == nil && expires.Equal(checked.AddDate(0, 0, 60))
}

func TestFAQPersistenceFailuresAndInvalidStates(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		service := testService(t)
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq", `{"question":"Question","answer":"Answer"}`), http.StatusInternalServerError)
	})
	t.Run("approve invalid state", func(t *testing.T) {
		service := testService(t)
		service.state.FAQ = []FAQEntry{{ID: "FAQ-0001", Status: "draft"}}
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/FAQ-0001/approve", ""), http.StatusConflict)
	})
	t.Run("approve persistence", func(t *testing.T) {
		service := testService(t)
		service.state.FAQ = []FAQEntry{{ID: "FAQ-0001", Status: "pending"}}
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/FAQ-0001/approve", ""), http.StatusInternalServerError)
	})
	t.Run("revoke missing or unapproved", func(t *testing.T) {
		service := testService(t)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/missing/revoke", ""), http.StatusNotFound)
		service.state.FAQ = []FAQEntry{{ID: "FAQ-0001", Status: "pending"}}
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/FAQ-0001/revoke", ""), http.StatusConflict)
	})
	t.Run("revoke persistence", func(t *testing.T) {
		service := testService(t)
		service.state.FAQ = []FAQEntry{{ID: "FAQ-0001", Status: "approved"}}
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodPost, "/mock/faq/FAQ-0001/revoke", ""), http.StatusInternalServerError)
	})
	t.Run("state expiry persistence", func(t *testing.T) {
		service := testService(t)
		service.state.FAQ = []FAQEntry{{ID: "FAQ-0001", Status: "approved", ExpiresAt: "2000-01-01"}}
		blockPersistence(t, service)
		statusIs(t, callService(t, service, http.MethodGet, "/mock/state", ""), http.StatusInternalServerError)
	})
}

func TestAnalyticsValidationAndRetention(t *testing.T) {
	service := testService(t)
	for _, body := range []string{
		`{`,
		`{"type":"unknown","role":"intern"}`,
		`{"type":"answer_shown","role":"guest"}`,
		`{"type":"answer_shown","role":"intern","request_id":"bad id"}`,
		`{"type":"answer_shown","role":"intern","request_id":"` + strings.Repeat("a", 65) + `"}`,
		`{"type":"answer_shown","role":"intern","category":"` + strings.Repeat("a", 41) + `"}`,
		`{"type":"answer_shown","role":"intern","status":"` + strings.Repeat("a", 41) + `"}`,
		`{"type":"answer_shown","role":"intern","source_id":"` + strings.Repeat("a", 41) + `"}`,
		`{"type":"answer_shown","role":"intern","reason":"` + strings.Repeat("a", 61) + `"}`,
	} {
		statusIs(t, callService(t, service, http.MethodPost, "/mock/events", body), http.StatusBadRequest)
	}

	statusIs(t, callService(t, service, http.MethodPost, "/mock/events",
		`{"type":"user_feedback","role":"intern","status":"helpful","request_id":"demo-1"}`), http.StatusCreated)
	if service.state.Events[0].At == "" {
		t.Fatal("event timestamp was not assigned")
	}
	service.state.Events = make([]AnalyticsEvent, 500)
	for i := range service.state.Events {
		service.state.Events[i].RequestID = "existing"
	}
	statusIs(t, callService(t, service, http.MethodPost, "/mock/events",
		`{"type":"session_started","role":"manager","request_id":"new_event"}`), http.StatusCreated)
	if len(service.state.Events) != 500 || service.state.Events[499].RequestID != "new_event" {
		t.Fatalf("event history was not capped: len=%d last=%+v", len(service.state.Events), service.state.Events[len(service.state.Events)-1])
	}
}

func TestPersistenceFailuresOnPlanStepsAndEvents(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(*Service)
	}{
		{"step", http.MethodPost, "/mock/steps/0-0", `{"done":true}`, nil},
		{"event", http.MethodPost, "/mock/events", `{"type":"session_started","role":"intern"}`, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := testService(t)
			if test.setup != nil {
				test.setup(service)
			}
			blockPersistence(t, service)
			statusIs(t, callService(t, service, test.method, test.path, test.body), http.StatusInternalServerError)
		})
	}
}

func TestDecodeRejectsUnknownExtraAndOversizedBodies(t *testing.T) {
	service := testService(t)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq", `{"question":"Q","answer":"A","unexpected":true}`), http.StatusBadRequest)
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq", `{"question":"Q","answer":"A"} {}`), http.StatusBadRequest)
	body := `{"question":"` + strings.Repeat("q", 17<<10) + `","answer":"A"}`
	statusIs(t, callService(t, service, http.MethodPost, "/mock/faq", body), http.StatusBadRequest)
}

func TestGetStateDoesNotRewriteUnexpiredFAQ(t *testing.T) {
	service := testService(t)
	service.state.FAQ = []FAQEntry{{ID: "FAQ-0001", Status: "approved", ExpiresAt: time.Now().AddDate(0, 0, 2).Format("2006-01-02")}}
	statusIs(t, callService(t, service, http.MethodGet, "/mock/state", ""), http.StatusOK)
	if service.state.FAQ[0].Status != "approved" {
		t.Fatalf("unexpired FAQ status changed: %+v", service.state.FAQ[0])
	}
}
