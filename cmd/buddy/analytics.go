package main

import (
	"net/http"

	"ai-buddy-avito/internal/mockcorp"
)

func (a *app) recordEvent(w http.ResponseWriter, r *http.Request, actor session) {
	var in struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Category  string `json:"category"`
		Status    string `json:"status"`
		SourceID  string `json:"source_id"`
		Reason    string `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/events", mockcorp.AnalyticsEvent{
		Type: in.Type, RequestID: in.RequestID, Role: actor.Role,
		Category: in.Category, Status: in.Status, SourceID: in.SourceID, Reason: in.Reason,
	})
}

func analyticsSummary(events []mockcorp.AnalyticsEvent) map[string]int {
	counts := map[string]int{"requests": 0, "answers": 0, "handoffs": 0, "accepted": 0, "helpful": 0, "fallbacks": 0}
	for _, event := range events {
		switch event.Type {
		case "request_created":
			counts["requests"]++
		case "answer_shown":
			counts["answers"]++
		case "human_gate_triggered":
			counts["handoffs"]++
		case "human_handoff_completed":
			counts["accepted"]++
		case "fallback_triggered":
			counts["fallbacks"]++
		case "user_feedback":
			if event.Status == "helpful" {
				counts["helpful"]++
			}
		}
	}
	return counts
}
