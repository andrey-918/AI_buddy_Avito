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
	"ai-buddy-avito/internal/mockcorp"
)

func (a *app) createHandoff(w http.ResponseWriter, r *http.Request, actor session) {
	var in struct {
		Topic string `json:"topic"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Topic != "merge" && in.Topic != "staging" && in.Topic != "unknown" {
		apiFail(w, http.StatusBadRequest, "invalid handoff topic")
		return
	}
	owner := "Наставник команды"
	if in.Topic == "staging" {
		owner = "Platform Tools"
	}
	a.forward(w, r, http.MethodPost, "/mock/handoffs", map[string]string{"topic": in.Topic, "owner": owner})
}

func (a *app) acceptHandoff(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "mentor") {
		return
	}
	var state mockcorp.State
	status, err := a.mock(r.Context(), http.MethodGet, "/mock/state", nil, &state)
	if err != nil || status != http.StatusOK {
		apiFail(w, http.StatusBadGateway, "mock service unavailable")
		return
	}
	for _, handoff := range state.Handoffs {
		if handoff.ID == r.PathValue("id") {
			if handoff.Owner != "Наставник команды" {
				apiFail(w, http.StatusForbidden, "handoff belongs to another owner")
				return
			}
			a.forward(w, r, http.MethodPost, "/mock/handoffs/"+handoff.ID+"/accept", nil)
			return
		}
	}
	apiFail(w, http.StatusNotFound, "handoff not found")
}

func (a *app) createFAQ(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "mentor") {
		return
	}
	var in struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !assistant.ScreenQuestion(in.Question).Allowed || !assistant.ScreenQuestion(in.Answer).Allowed {
		apiFail(w, http.StatusBadRequest, "FAQ contains unsupported or sensitive content")
		return
	}
	if a.knowledge == nil {
		apiFail(w, http.StatusServiceUnavailable, "knowledge unavailable")
		return
	}
	var state mockcorp.State
	status, err := a.mock(r.Context(), http.MethodGet, "/mock/state", nil, &state)
	if err != nil || status != http.StatusOK {
		apiFail(w, http.StatusBadGateway, "mock service unavailable")
		return
	}
	cards := []map[string]string{}
	for _, card := range a.knowledge.ReviewContext("mentor", "backend-platform-demo") {
		cards = append(cards, map[string]string{"id": card.ID, "title": card.Title, "evidence_text": card.EvidenceText, "answer": card.Answer})
	}
	for _, faq := range state.FAQ {
		cards = append(cards, map[string]string{"id": faq.ID, "title": faq.Question, "evidence_text": faq.Answer, "answer": faq.Answer})
	}
	payload, _ := json.Marshal(map[string]any{"question": in.Question, "cards": cards})
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, env("BUDDY_PYTHON", "python3"), env("BUDDY_DUP_SCRIPT", "ml/duplicates.py"))
	command.Stdin = bytes.NewReader(payload)
	command.Stderr = io.Discard
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		apiFail(w, http.StatusServiceUnavailable, "duplicate search unavailable")
		return
	}
	var result struct {
		CandidateIDs []string `json:"candidate_ids"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		apiFail(w, http.StatusBadGateway, "invalid duplicate search response")
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/faq", map[string]any{"question": in.Question, "answer": in.Answer, "duplicate_ids": result.CandidateIDs})
}

func (a *app) approveFAQ(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "mentor") {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/faq/"+r.PathValue("id")+"/approve", nil)
}

func (a *app) revokeFAQ(w http.ResponseWriter, r *http.Request, actor session) {
	if !requireRole(w, actor, "mentor") {
		return
	}
	a.forward(w, r, http.MethodPost, "/mock/faq/"+r.PathValue("id")+"/revoke", nil)
}
