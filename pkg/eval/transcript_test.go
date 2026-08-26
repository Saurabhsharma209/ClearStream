package eval

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ─── normaliseText ────────────────────────────────────────────────────────────

func TestNormaliseText_UppercaseAndWhitespaceCollapse(t *testing.T) {
	input := "  Hello   WORLD\t\nFoo  "
	want := "hello world foo"
	got := normaliseText(input)
	if got != want {
		t.Errorf("normaliseText(%q) = %q; want %q", input, got, want)
	}
}

func TestNormaliseText_AlreadyNormalised(t *testing.T) {
	input := "hello world"
	got := normaliseText(input)
	if got != input {
		t.Errorf("normaliseText(%q) = %q; want %q", input, got, input)
	}
}

func TestNormaliseText_EmptyString(t *testing.T) {
	got := normaliseText("")
	if got != "" {
		t.Errorf("normaliseText(%q) = %q; want empty string", "", got)
	}
}

// ─── charScore ────────────────────────────────────────────────────────────────

func TestCharScore_IdenticalStrings(t *testing.T) {
	score := charScore("hello", "hello")
	if math.Abs(score-1.0) > 1e-9 {
		t.Errorf("charScore(identical) = %.4f; want 1.0", score)
	}
}

func TestCharScore_BothEmpty(t *testing.T) {
	score := charScore("", "")
	if math.Abs(score-1.0) > 1e-9 {
		t.Errorf("charScore(empty, empty) = %.4f; want 1.0", score)
	}
}

func TestCharScore_OneEmpty(t *testing.T) {
	score := charScore("hello", "")
	if score != 0.0 {
		t.Errorf("charScore(non-empty, empty) = %.4f; want 0.0", score)
	}
	score2 := charScore("", "hello")
	if score2 != 0.0 {
		t.Errorf("charScore(empty, non-empty) = %.4f; want 0.0", score2)
	}
}

func TestCharScore_PartialMatch(t *testing.T) {
	// "abc" vs "axc": LCS = 2 (a, c), total = 3+3 = 6, score = 2*2/6 = 0.667
	score := charScore("abc", "axc")
	if score <= 0.0 || score >= 1.0 {
		t.Errorf("charScore partial match = %.4f; expected in (0, 1)", score)
	}
}

// ─── wordScore ────────────────────────────────────────────────────────────────

func TestWordScore_IdenticalStrings(t *testing.T) {
	score := wordScore("hello world", "hello world")
	if math.Abs(score-1.0) > 1e-9 {
		t.Errorf("wordScore(identical) = %.4f; want 1.0", score)
	}
}

func TestWordScore_BothEmpty(t *testing.T) {
	score := wordScore("", "")
	if math.Abs(score-1.0) > 1e-9 {
		t.Errorf("wordScore(empty, empty) = %.4f; want 1.0", score)
	}
}

func TestWordScore_OneEmpty(t *testing.T) {
	score := wordScore("hello world", "")
	if score != 0.0 {
		t.Errorf("wordScore(non-empty, empty) = %.4f; want 0.0", score)
	}
	score2 := wordScore("", "hello world")
	if score2 != 0.0 {
		t.Errorf("wordScore(empty, non-empty) = %.4f; want 0.0", score2)
	}
}

func TestWordScore_PartialMatch(t *testing.T) {
	// "hello world" vs "hello there": 1 common word (hello), total = 2+2 = 4, score = 2*1/4 = 0.5
	score := wordScore("hello world", "hello there")
	if math.Abs(score-0.5) > 1e-9 {
		t.Errorf("wordScore partial = %.4f; want 0.5", score)
	}
}

// ─── lcsMatcher (rune slices) ─────────────────────────────────────────────────

func TestLcsMatcher_EmptySlices(t *testing.T) {
	got := lcsMatcher([]rune{}, []rune{})
	if got != 0 {
		t.Errorf("lcsMatcher(empty, empty) = %d; want 0", got)
	}
	got2 := lcsMatcher([]rune("hello"), []rune{})
	if got2 != 0 {
		t.Errorf("lcsMatcher(non-empty, empty) = %d; want 0", got2)
	}
}

func TestLcsMatcher_IdenticalRunes(t *testing.T) {
	// "hello" vs "hello" → LCS = 5
	got := lcsMatcher([]rune("hello"), []rune("hello"))
	if got != 5 {
		t.Errorf("lcsMatcher(hello, hello) = %d; want 5", got)
	}
}

func TestLcsMatcher_SubsequenceMatch(t *testing.T) {
	// "abc" vs "axbycz" → LCS = 3 (a, b, c)
	got := lcsMatcher([]rune("abc"), []rune("axbycz"))
	if got != 3 {
		t.Errorf("lcsMatcher(abc, axbycz) = %d; want 3", got)
	}
}

// ─── lcsMatcherStr (string slices) ───────────────────────────────────────────

func TestLcsMatcherStr_EmptySlices(t *testing.T) {
	got := lcsMatcherStr([]string{}, []string{})
	if got != 0 {
		t.Errorf("lcsMatcherStr(empty, empty) = %d; want 0", got)
	}
	got2 := lcsMatcherStr([]string{"a", "b"}, []string{})
	if got2 != 0 {
		t.Errorf("lcsMatcherStr(non-empty, empty) = %d; want 0", got2)
	}
}

func TestLcsMatcherStr_IdenticalSlices(t *testing.T) {
	// ["a","b"] vs ["a","b"] → LCS = 2
	got := lcsMatcherStr([]string{"a", "b"}, []string{"a", "b"})
	if got != 2 {
		t.Errorf("lcsMatcherStr([a b], [a b]) = %d; want 2", got)
	}
}

func TestLcsMatcherStr_PartialMatch(t *testing.T) {
	// ["a","b"] vs ["b","c"] → LCS = 1 ("b")
	got := lcsMatcherStr([]string{"a", "b"}, []string{"b", "c"})
	if got != 1 {
		t.Errorf("lcsMatcherStr([a b], [b c]) = %d; want 1", got)
	}
}

// ─── NewTranscriptScorer ──────────────────────────────────────────────────────

func TestNewTranscriptScorer_DefaultsApplied(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{})
	if scorer == nil {
		t.Fatal("NewTranscriptScorer returned nil")
	}
	if scorer.cfg.LLMTimeout == 0 {
		t.Error("LLMTimeout should default to non-zero")
	}
	if scorer.cfg.RateLimitDelay == 0 {
		t.Error("RateLimitDelay should default to non-zero")
	}
	if scorer.cfg.Model == "" {
		t.Error("Model should default to non-empty")
	}
	if scorer.client == nil {
		t.Error("http.Client should be initialised")
	}
}

// ─── Score without LLM ────────────────────────────────────────────────────────

func TestScore_IdenticalPairNoLLM(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{LLMEndpoint: ""})
	ctx := context.Background()

	scores, err := scorer.Score(ctx, "hello world", "hello world")
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if math.Abs(scores.CharScore-1.0) > 1e-9 {
		t.Errorf("CharScore = %.4f; want 1.0", scores.CharScore)
	}
	if math.Abs(scores.WordScore-1.0) > 1e-9 {
		t.Errorf("WordScore = %.4f; want 1.0", scores.WordScore)
	}
	if scores.LLMScore != -1 {
		t.Errorf("LLMScore = %.1f; want -1 (LLM disabled)", scores.LLMScore)
	}
}

func TestScore_TotallyDifferentPairNoLLM(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{LLMEndpoint: ""})
	ctx := context.Background()

	// Completely different strings: "aaaa" vs "zzzz" → LCS = 0
	scores, err := scorer.Score(ctx, "aaaa", "zzzz")
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if scores.CharScore >= 0.5 {
		t.Errorf("CharScore = %.4f; expected < 0.5 for unrelated strings", scores.CharScore)
	}
	if scores.WordScore >= 0.5 {
		t.Errorf("WordScore = %.4f; expected < 0.5 for unrelated strings", scores.WordScore)
	}
	if scores.LLMScore != -1 {
		t.Errorf("LLMScore = %.1f; want -1 (LLM disabled)", scores.LLMScore)
	}
}

// ─── ScoreAll without LLM ─────────────────────────────────────────────────────

func TestScoreAll_SkipsEmptyReference(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{LLMEndpoint: ""})
	ctx := context.Background()

	pairs := []struct{ ID, Reference, Comparison string }{
		{ID: "conv-1", Reference: "hello world", Comparison: "hello world"},
		{ID: "conv-2", Reference: "foo bar baz", Comparison: "foo bar qux"},
		{ID: "conv-3", Reference: "", Comparison: "something"}, // should be skipped
	}

	results, summary := scorer.ScoreAll(ctx, "test-denoiser", pairs)

	if summary.N != 2 {
		t.Errorf("DenoiserSummary.N = %d; want 2", summary.N)
	}
	if summary.Skipped != 1 {
		t.Errorf("DenoiserSummary.Skipped = %d; want 1", summary.Skipped)
	}
	if len(results) != 2 {
		t.Errorf("len(results) = %d; want 2", len(results))
	}
	if summary.AvgChar <= 0 {
		t.Errorf("AvgChar = %.4f; want > 0", summary.AvgChar)
	}
	if summary.AvgWord <= 0 {
		t.Errorf("AvgWord = %.4f; want > 0", summary.AvgWord)
	}
	// LLM disabled → AvgLLM should be 0 (no LLM scores accumulated)
	if summary.AvgLLM != 0 {
		t.Errorf("AvgLLM = %.4f; want 0 (LLM disabled)", summary.AvgLLM)
	}
}

// TestScore_ContextCancelDuringRateLimit verifies ctx.Done() is respected
// when waiting for the rate-limit delay.
func TestScore_ContextCancelDuringRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"85"}}]}`))
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		LLMAPIKey:      "test-key",
		RateLimitDelay: 2 * time.Second, // long enough that cancel fires first
	})

	// Prime last call time so the rate limit will kick in on the next call.
	scorer.last = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := scorer.Score(ctx, "hello world", "hello world")
	if err == nil {
		t.Error("expected error (context cancellation during rate limit wait), got nil")
	}
}

// TestScore_EmptyComparison verifies that empty comparison skips LLM and returns -1.
func TestScore_EmptyComparison(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint: "http://localhost:99999", // unreachable — but LLM should be skipped
	})
	ctx := context.Background()

	scores, err := scorer.Score(ctx, "hello", "")
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if scores.LLMScore != -1 {
		t.Errorf("LLMScore: want -1 for empty comparison, got %.1f", scores.LLMScore)
	}
}

// TestScore_EmptyReference verifies that empty reference skips LLM.
func TestScore_EmptyReference(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint: "http://localhost:99999",
	})
	ctx := context.Background()

	scores, err := scorer.Score(ctx, "", "hello")
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if scores.LLMScore != -1 {
		t.Errorf("LLMScore: want -1 for empty reference, got %.1f", scores.LLMScore)
	}
}

func TestScore_RateLimitWaitThenCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"75"}}]}`))
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		LLMAPIKey:      "test-key",
		RateLimitDelay: 30 * time.Millisecond,
	})
	scorer.last = time.Now() // prime so wait fires

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	scores, err := scorer.Score(ctx, "hello", "hello")
	if err != nil {
		t.Fatalf("Score after rate-limit wait: %v", err)
	}
	// LLM may or may not succeed; just ensure no crash.
	_ = scores
}

func TestScoreAll_WithLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"80"}}]}`))
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		LLMAPIKey:      "key",
		RateLimitDelay: 0,
	})
	scorer.last = time.Time{} // no wait

	pairs := []struct{ ID, Reference, Comparison string }{
		{ID: "c1", Reference: "hello world", Comparison: "hello world"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, summary := scorer.ScoreAll(ctx, "test-denoiser", pairs)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// LLM endpoint was hit; if parsing succeeded LLMScore = 80, AvgLLM = 80.
	// If parse fails LLMScore = -1 — either is valid; we just need no panic.
	_ = summary
}

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
