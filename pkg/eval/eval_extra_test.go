package eval

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── max ──────────────────────────────────────────────────────────────────────

func TestMax_FirstGreater(t *testing.T) {
	if got := max(7, 2); got != 7 {
		t.Errorf("max(7,2) = %d; want 7", got)
	}
}

func TestMax_SecondGreater(t *testing.T) {
	if got := max(3, 5); got != 5 {
		t.Errorf("max(3,5) = %d; want 5", got)
	}
}

// ─── llmScore error paths ─────────────────────────────────────────────────────

func TestLLMScore_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"choices\":[]}")
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		RateLimitDelay: 1,
	})
	_, err := scorer.llmScore(context.Background(), "hello world", "hello world")
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("expected 'no choices' in error, got: %v", err)
	}
}

func TestLLMScore_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not-json{{{")
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		RateLimitDelay: 1,
	})
	_, err := scorer.llmScore(context.Background(), "hello", "hello")
	if err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
}

func TestLLMScore_BadScoreContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"choices\":[{\"message\":{\"content\":\"not-a-number\"}}]}")
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		RateLimitDelay: 1,
	})
	_, err := scorer.llmScore(context.Background(), "hello", "hello")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse LLM score") {
		t.Errorf("expected 'parse LLM score' in error, got: %v", err)
	}
}

func TestLLMScore_ValidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"choices\":[{\"message\":{\"content\":\"85\"}}]}")
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		RateLimitDelay: 1,
	})
	score, err := scorer.llmScore(context.Background(), "hello world", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 85.0 {
		t.Errorf("score: want 85.0, got %.1f", score)
	}
}
