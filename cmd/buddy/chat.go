package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"time"

	"ai-buddy-avito/internal/assistant"
	"ai-buddy-avito/internal/knowledge"
	"ai-buddy-avito/internal/mockcorp"
)

type modelDecision struct {
	Outcome string   `json:"outcome"`
	Intent  string   `json:"intent"`
	Reason  string   `json:"reason"`
	Owner   string   `json:"owner"`
	Text    string   `json:"text"`
	Mode    string   `json:"mode"`
	CardID  string   `json:"card_id"`
	Score   *float64 `json:"retrieval_score"`
}

func (a *app) chatReply(w http.ResponseWriter, r *http.Request, actor session) {
	var input struct {
		Question string `json:"question"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	guard := assistant.ScreenQuestion(input.Question)
	if !guard.Allowed {
		jsonReply(w, http.StatusOK, map[string]any{"outcome": "fallback", "reason": guard.Reason, "text": guard.Fallback})
		return
	}
	if a.knowledge == nil {
		apiFail(w, http.StatusServiceUnavailable, "knowledge unavailable")
		return
	}
	cards := a.knowledge.EligibleContext(actor.Role, "backend-platform-demo", time.Now(), true)
	var state mockcorp.State
	status, err := a.mock(r.Context(), http.MethodGet, "/mock/state", nil, &state)
	if err != nil || status != http.StatusOK {
		apiFail(w, http.StatusBadGateway, "mock service unavailable")
		return
	}
	moscow, _ := time.LoadLocation("Europe/Moscow")
	today := time.Now().In(moscow).Format("2006-01-02")
	for _, faq := range state.FAQ {
		if faq.Status == "approved" && faq.ExpiresAt >= today {
			cards = append(cards, knowledge.ContextRecord{
				ID: faq.ID, Title: faq.Question, EvidenceText: faq.Answer,
				Answer: faq.Answer, AnswerScope: "full",
				Source: knowledge.Source{Title: "Проверенный FAQ наставника", Locator: faq.ID, EvidenceKind: "mentor_verified"},
			})
		}
	}
	if len(cards) == 0 {
		jsonReply(w, http.StatusOK, map[string]any{"outcome": "fallback", "reason": "no_eligible_knowledge", "text": "Нет доступных проверенных знаний. Передайте вопрос наставнику."})
		return
	}
	payload, _ := json.Marshal(map[string]any{"question": input.Question, "cards": cards})
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, env("BUDDY_PYTHON", "python3"), env("BUDDY_ML_SCRIPT", "ml/runtime.py"))
	command.Stdin = bytes.NewReader(payload)
	command.Stderr = io.Discard // Provider errors can contain prompt fragments.
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		jsonReply(w, http.StatusOK, map[string]any{"outcome": "fallback", "reason": "ml_unavailable", "text": "Поиск сейчас недоступен. Передайте вопрос наставнику."})
		return
	}
	var decision modelDecision
	if err := json.Unmarshal(output.Bytes(), &decision); err != nil {
		apiFail(w, http.StatusBadGateway, "invalid ML response")
		return
	}
	if decision.Outcome == "answer" {
		var allowed bool
		for _, card := range cards {
			if card.ID == decision.CardID {
				allowed = true
				break
			}
		}
		if !allowed {
			apiFail(w, http.StatusBadGateway, "ML selected unavailable source")
			return
		}
		card, ok := a.knowledge.RecordByID(decision.CardID)
		if ok {
			// The displayed answer comes from the approved card, never free-form model text.
			jsonReply(w, http.StatusOK, map[string]any{"outcome": "answer", "intent": card.Category, "mode": decision.Mode, "text": card.Answer, "source": sourceView(card)})
			return
		}
		for _, faq := range state.FAQ {
			if faq.ID == decision.CardID && faq.Status == "approved" && faq.ExpiresAt >= today {
				jsonReply(w, http.StatusOK, map[string]any{
					"outcome": "answer", "intent": "other", "mode": decision.Mode, "text": faq.Answer,
					"source": map[string]any{"id": faq.ID, "source": "Проверенный FAQ наставника", "uri": "", "owner": faq.OwnerID, "checked": faq.CheckedAt, "expires": faq.ExpiresAt, "origin": "mentor_verified"},
				})
				return
			}
		}
		apiFail(w, http.StatusBadGateway, "knowledge source missing")
		return
	}
	if decision.Outcome != "human_gate" && decision.Outcome != "fallback" {
		apiFail(w, http.StatusBadGateway, "invalid ML outcome")
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"outcome": decision.Outcome, "intent": decision.Intent, "reason": decision.Reason,
		"owner": decision.Owner, "text": decision.Text, "mode": decision.Mode,
	})
}

func sourceView(card knowledge.Record) map[string]any {
	return map[string]any{
		"id": card.ID, "source": card.Source.Title, "uri": card.Source.URI,
		"owner": card.OwnerID, "checked": card.CheckedAt, "expires": card.ExpiresAt,
		"origin": card.DataOrigin, "locator": card.Source.Locator,
	}
}
