package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ─── Transcript comparison — matches Exotel VoiceBot eval framework ───────────
//
// Three metrics, identical to denoiser_analysis.py:
//
//	Char  — character-level SequenceMatcher ratio on normalised text
//	Word  — word-level SequenceMatcher ratio on normalised tokenisation
//	LLM   — semantic similarity scored 0–100 via OpenAI-compatible API
//
// Usage:
//
//	scorer := eval.NewTranscriptScorer(eval.TranscriptScorerConfig{
//	    LLMEndpoint:   "https://<azure-openai-endpoint>/openai/deployments/voice-bot-gpt-35-1106/chat/completions?api-version=2024-02-01",
//	    LLMAPIKey:     os.Getenv("AZURE_OPENAI_API_KEY"),
//	    LLMTimeout:    30 * time.Second,
//	    RateLimitDelay: time.Second,
//	})
//	result, err := scorer.Score(ctx, reference, comparison)

// ─── Types ────────────────────────────────────────────────────────────────────

// TranscriptScores holds the three scores for one (reference, comparison) pair.
type TranscriptScores struct {
	// CharScore is the character-level SequenceMatcher ratio (0.0–1.0).
	// Sensitive to typos and small deletions; penalises "nine" vs "9".
	CharScore float64 `json:"char_score"`

	// WordScore is the word-level SequenceMatcher ratio (0.0–1.0).
	// Less sensitive to single-char typos; still order-sensitive.
	WordScore float64 `json:"word_score"`

	// LLMScore is the semantic similarity rating from an LLM (0–100).
	// Lenient on minor rephrasing / number format differences.
	// Penalises hallucinations and background noise insertion.
	// -1 if LLM scoring is disabled or failed.
	LLMScore float64 `json:"llm_score"`
}

// TranscriptResult is a single (conversation, denoiser) comparison result.
// Matches the column layout of denoiser_results.md from the VoiceBot team.
type TranscriptResult struct {
	ConversationID string           `json:"conversation_id"`
	Denoiser       string           `json:"denoiser"`
	Reference      string           `json:"reference"`
	Comparison     string           `json:"comparison"`
	Scores         TranscriptScores `json:"scores"`
	Error          string           `json:"error,omitempty"`
}

// DenoiserSummary aggregates per-conversation results into per-denoiser averages.
type DenoiserSummary struct {
	Denoiser    string  `json:"denoiser"`
	N           int     `json:"n"`
	AvgChar     float64 `json:"avg_char_pct"`
	AvgWord     float64 `json:"avg_word_pct"`
	AvgLLM      float64 `json:"avg_llm_score"`
	Skipped     int     `json:"skipped"`
}

// ─── VAD + WER per-file schema ────────────────────────────────────────────────
// Matches the TSV/CSV columns from the Exotel eval pipeline.

// VADEvalRow is one row from the per-file VAD evaluation output.
// Column order matches: filename, language, gt_transcript, original_duration,
// speech_start_time, speech_end_time, noise_type, snr_level, stt_transcript,
// vad_starts, vad_stops, first_vad_start, last_vad_stop,
// delta_vad_start, delta_vad_end, wer
type VADEvalRow struct {
	Filename         string  `json:"filename"`
	Language         string  `json:"language"`
	GTTranscript     string  `json:"gt_transcript"`
	OriginalDuration float64 `json:"original_duration_s"`
	SpeechStartTime  float64 `json:"speech_start_time_s"`
	SpeechEndTime    float64 `json:"speech_end_time_s"`
	NoiseType        string  `json:"noise_type"`
	SNRLevel         float64 `json:"snr_level_db"`
	STTTranscript    string  `json:"stt_transcript"`
	VADStarts        []float64 `json:"vad_starts"`
	VADStops         []float64 `json:"vad_stops"`
	FirstVADStart    float64 `json:"first_vad_start_s"`
	LastVADStop      float64 `json:"last_vad_stop_s"`
	DeltaVADStart    float64 `json:"delta_vad_start_s"` // first_vad_start - speech_start_time
	DeltaVADEnd      float64 `json:"delta_vad_end_s"`   // last_vad_stop - speech_end_time
	WER              float64 `json:"wer"`
}

// DenoiserAggRow matches: file, denoiser, tt_model, n_total, n_vad_fired,
// true_nega, vad_miss_rate, wer, n_wer_rows, vad_start_delta_mean/std/p90,
// vad_end_delta_mean/std/p90, tt_end_delta_mean/std/p90,
// tt_inference_ms_mean/p90, tt_ml_pct, tt_ml_prob_mean
type DenoiserAggRow struct {
	File              string  `json:"file"`
	Denoiser          string  `json:"denoiser"`
	TTModel           string  `json:"tt_model"`
	NTotal            int     `json:"n_total"`
	NVADFired         int     `json:"n_vad_fired"`
	TrueNega          int     `json:"true_nega"`
	VADMissRate       float64 `json:"vad_miss_rate"`
	WER               float64 `json:"wer"`
	NWERRows          int     `json:"n_wer_rows"`
	VADStartDeltaMean float64 `json:"vad_start_delta_mean"`
	VADStartDeltaStd  float64 `json:"vad_start_delta_std"`
	VADStartDeltaP90  float64 `json:"vad_start_delta_p90"`
	VADEndDeltaMean   float64 `json:"vad_end_delta_mean"`
	VADEndDeltaStd    float64 `json:"vad_end_delta_std"`
	VADEndDeltaP90    float64 `json:"vad_end_delta_p90"`
	TTInferenceMsMean float64 `json:"tt_inference_ms_mean"`
	TTInferenceMsP90  float64 `json:"tt_inference_ms_p90"`
	TTMLPct           float64 `json:"tt_ml_pct"`
	TTMLProbMean      float64 `json:"tt_ml_prob_mean"`
}

// GroupSummaryRow matches:
// group, subgroup, experiment, mean_delta_vad_start, p90_delta_vad_start,
// mean_delta_vad_end, p90_delta_vad_end, mean_wer,
// n_valid_vad_start, n_valid_vad_end, n_valid_wer
type GroupSummaryRow struct {
	Group             string  `json:"group"`
	Subgroup          string  `json:"subgroup"`
	Experiment        string  `json:"experiment"`
	MeanDeltaVADStart float64 `json:"mean_delta_vad_start"`
	P90DeltaVADStart  float64 `json:"p90_delta_vad_start"`
	MeanDeltaVADEnd   float64 `json:"mean_delta_vad_end"`
	P90DeltaVADEnd    float64 `json:"p90_delta_vad_end"`
	MeanWER           float64 `json:"mean_wer"`
	NValidVADStart    int     `json:"n_valid_vad_start"`
	NValidVADEnd      int     `json:"n_valid_vad_end"`
	NValidWER         int     `json:"n_valid_wer"`
}

// ─── Scorer ───────────────────────────────────────────────────────────────────

// TranscriptScorerConfig configures the scorer.
type TranscriptScorerConfig struct {
	// LLMEndpoint is the Azure OpenAI / OpenAI chat completions URL.
	// If empty, LLM scoring is disabled and LLMScore = -1.
	LLMEndpoint string

	// LLMAPIKey is the bearer token / API key.
	LLMAPIKey string

	// LLMTimeout is the per-request timeout for LLM calls. Default: 30s.
	LLMTimeout time.Duration

	// RateLimitDelay is sleep between LLM calls to avoid throttling.
	// Matches the 1s delay in denoiser_analysis.py. Default: 1s.
	RateLimitDelay time.Duration

	// Model is the deployment/model name. Default: "gpt-3.5-turbo".
	Model string
}

// TranscriptScorer scores transcript pairs using Char, Word, and LLM metrics.
type TranscriptScorer struct {
	cfg    TranscriptScorerConfig
	client *http.Client
	last   time.Time // for rate limiting
}

// NewTranscriptScorer creates a TranscriptScorer.
func NewTranscriptScorer(cfg TranscriptScorerConfig) *TranscriptScorer {
	if cfg.LLMTimeout == 0 {
		cfg.LLMTimeout = 30 * time.Second
	}
	if cfg.RateLimitDelay == 0 {
		cfg.RateLimitDelay = time.Second
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-3.5-turbo"
	}
	return &TranscriptScorer{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.LLMTimeout},
	}
}

// Score computes Char, Word, and LLM scores for one (reference, comparison) pair.
func (s *TranscriptScorer) Score(ctx context.Context, reference, comparison string) (TranscriptScores, error) {
	ref := normaliseText(reference)
	cmp := normaliseText(comparison)

	scores := TranscriptScores{
		CharScore: charScore(ref, cmp),
		WordScore: wordScore(ref, cmp),
		LLMScore:  -1,
	}

	if s.cfg.LLMEndpoint != "" && reference != "" && comparison != "" {
		// Rate limit
		if wait := s.cfg.RateLimitDelay - time.Since(s.last); wait > 0 {
			select {
			case <-ctx.Done():
				return scores, ctx.Err()
			case <-time.After(wait):
			}
		}
		llm, err := s.llmScore(ctx, reference, comparison)
		s.last = time.Now()
		if err == nil {
			scores.LLMScore = llm
		}
	}

	return scores, nil
}

// ScoreAll scores a slice of (conversationID, reference, comparison) triples
// and returns per-result rows + a per-denoiser summary.
func (s *TranscriptScorer) ScoreAll(
	ctx context.Context,
	denoiser string,
	pairs []struct{ ID, Reference, Comparison string },
) ([]TranscriptResult, DenoiserSummary) {
	results := make([]TranscriptResult, 0, len(pairs))
	var sumChar, sumWord, sumLLM float64
	var skipped, nLLM int

	for _, p := range pairs {
		if p.Reference == "" || p.Comparison == "" {
			skipped++
			continue
		}
		scores, err := s.Score(ctx, p.Reference, p.Comparison)
		r := TranscriptResult{
			ConversationID: p.ID,
			Denoiser:       denoiser,
			Reference:      p.Reference,
			Comparison:     p.Comparison,
			Scores:         scores,
		}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
		sumChar += scores.CharScore
		sumWord += scores.WordScore
		if scores.LLMScore >= 0 {
			sumLLM += scores.LLMScore
			nLLM++
		}
	}

	n := len(results)
	sum := DenoiserSummary{Denoiser: denoiser, N: n, Skipped: skipped}
	if n > 0 {
		sum.AvgChar = sumChar / float64(n) * 100
		sum.AvgWord = sumWord / float64(n) * 100
	}
	if nLLM > 0 {
		sum.AvgLLM = sumLLM / float64(nLLM)
	}
	return results, sum
}

// ─── Char / Word scoring ─────────────────────────────────────────────────────

var wsRE = regexp.MustCompile(`\s+`)

// normaliseText lowercases and collapses whitespace — same as denoiser_analysis.py.
func normaliseText(s string) string {
	return strings.TrimSpace(wsRE.ReplaceAllString(strings.ToLower(s), " "))
}

// charScore computes SequenceMatcher ratio on runes.
// ratio = 2 * M / T  where M = matching rune count, T = total runes in both strings.
func charScore(a, b string) float64 {
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1.0
	}
	total := utf8.RuneCountInString(a) + utf8.RuneCountInString(b)
	if total == 0 {
		return 0
	}
	m := lcsMatcher(ra, rb)
	return 2.0 * float64(m) / float64(total)
}

// wordScore computes SequenceMatcher ratio on word tokens.
func wordScore(a, b string) float64 {
	wa := strings.Fields(a)
	wb := strings.Fields(b)
	if len(wa) == 0 && len(wb) == 0 {
		return 1.0
	}
	total := len(wa) + len(wb)
	if total == 0 {
		return 0
	}
	m := lcsMatcherStr(wa, wb)
	return 2.0 * float64(m) / float64(total)
}

// lcsMatcher counts matching elements via LCS (longest common subsequence length).
// Approximates Python's SequenceMatcher matching blocks sum.
func lcsMatcher(a, b []rune) int {
	// Standard O(n*m) LCS — good enough for transcript lengths.
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return 0
	}
	// Use two rows to save memory.
	prev := make([]int, m+1)
	curr := make([]int, m+1)
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				if prev[j] > curr[j-1] {
					curr[j] = prev[j]
				} else {
					curr[j] = curr[j-1]
				}
			}
		}
		prev, curr = curr, prev
		for j := range curr {
			curr[j] = 0
		}
	}
	return prev[m]
}

func lcsMatcherStr(a, b []string) int {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return 0
	}
	prev := make([]int, m+1)
	curr := make([]int, m+1)
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				if prev[j] > curr[j-1] {
					curr[j] = prev[j]
				} else {
					curr[j] = curr[j-1]
				}
			}
		}
		prev, curr = curr, prev
		for j := range curr {
			curr[j] = 0
		}
	}
	return prev[m]
}

// ─── LLM scoring ─────────────────────────────────────────────────────────────

// llmScore calls the Azure OpenAI API with the same prompt as denoiser_analysis.py.
func (s *TranscriptScorer) llmScore(ctx context.Context, reference, comparison string) (float64, error) {
	systemPrompt := `You are evaluating the quality of a denoiser for voice-call transcripts.
You will be given a reference (golden) user transcript and a comparison transcript produced from denoised audio.
Rate how similar the comparison is to the reference on a scale of 0 to 100.

Rules:
- Be LENIENT on: minor rephrasing ("I'd like to" vs "I want to"), number format differences ("nine eight seven" vs "987"), small transcription errors, different punctuation.
- PENALISE LIGHTLY: minor word changes that preserve meaning.
- PENALISE HEAVILY: extra words from background noise, hallucinated content, completely wrong sentences, major omissions.
- Score 0: hallucinated / completely wrong content unrelated to the reference.
- Score 100: same meaning, same key words, no extra noise content.

Respond with ONLY an integer from 0 to 100. No explanation.`

	userMsg := fmt.Sprintf("Reference:\n%s\n\nComparison:\n%s", reference, comparison)

	body, _ := json.Marshal(map[string]interface{}{
		"model": s.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMsg},
		},
		"max_tokens":  5,
		"temperature": 0,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.LLMEndpoint, bytes.NewReader(body))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.LLMAPIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return -1, err
	}
	if len(out.Choices) == 0 {
		return -1, fmt.Errorf("eval: LLM returned no choices")
	}
	var score float64
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if _, err := fmt.Sscanf(content, "%f", &score); err != nil {
		return -1, fmt.Errorf("eval: parse LLM score %q: %w", content, err)
	}
	return score, nil
}
