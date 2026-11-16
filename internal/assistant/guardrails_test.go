package assistant

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The development split is used while building the filter. Holdout questions
// stay untouched until the complete assistant is evaluated.
func TestScreenQuestionDevelopmentCases(t *testing.T) {
	data, err := os.ReadFile("../../data/eval/questions.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID       string `json:"id"`
			Split    string `json:"split"`
			Question string `json:"question"`
			Expected struct {
				Reason           string `json:"reason"`
				ModelCallAllowed *bool  `json:"model_call_allowed"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, tc := range fixture.Cases {
		if tc.Split != "development" || tc.Expected.ModelCallAllowed == nil || *tc.Expected.ModelCallAllowed {
			continue
		}
		checked++
		t.Run(tc.ID, func(t *testing.T) {
			got := ScreenQuestion(tc.Question)
			if got.Allowed || got.Reason != tc.Expected.Reason || got.Fallback == "" {
				t.Fatalf("want blocked question with reason %q, got %+v", tc.Expected.Reason, got)
			}
			if strings.Contains(got.Fallback, tc.Question) {
				t.Fatal("fallback must not repeat the submitted question")
			}
		})
	}
	if checked != 4 {
		t.Fatalf("expected 4 blocked development cases, got %d", checked)
	}
}

func TestScreenQuestionTypicalRequests(t *testing.T) {
	cases := []struct {
		name     string
		question string
		allowed  bool
		reason   string
	}{
		{"repository access", "Как получить доступ к учебному backend-репозиторию?", true, ""},
		{"personal data policy", "Где прочитать политику защиты персональных данных?", true, ""},
		{"email address", "Мой адрес для заявки: intern@example.org", false, "sensitive_data"},
		{"phone number", "Перезвони мне по номеру +7 999 123-45-67", false, "sensitive_data"},
		{"financial action", "Оплати счёт через корпоративный сервис", false, "financial_operation"},
		{"development review", "Сделай код-ревью моей задачи", false, "excluded_scope"},
		{"empty question", "  ", false, "invalid_question"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScreenQuestion(tc.question)
			if got.Allowed != tc.allowed || got.Reason != tc.reason {
				t.Fatalf("want allowed=%t reason=%q, got %+v", tc.allowed, tc.reason, got)
			}
			if !got.Allowed && got.Fallback == "" {
				t.Fatal("a blocked question needs a safe fallback")
			}
		})
	}
}
