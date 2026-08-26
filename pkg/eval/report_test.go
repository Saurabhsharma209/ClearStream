package eval

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestWriteAllReports_EmptyFiles verifies WriteAllReports works with empty file list.
func TestWriteAllReports_EmptyFiles(t *testing.T) {
	dir := t.TempDir()
	summary := BatchSummary{
		TotalFiles:     0,
		ProcessedFiles: 0,
		Files:          []FileResult{},
	}
	csvPath, summPath, filesPath, cfgPath, err := WriteAllReports(dir, summary)
	if err != nil {
		t.Fatalf("WriteAllReports with empty files: %v", err)
	}
	for _, p := range []string{csvPath, summPath, filesPath, cfgPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected output file %s to exist: %v", p, err)
		}
	}
}

// TestWriteAllReports_InvalidDir verifies an error is returned for an unwritable dir.
func TestWriteAllReports_InvalidDir(t *testing.T) {
	_, _, _, _, err := WriteAllReports("/dev/null/nonexistent", BatchSummary{})
	if err == nil {
		t.Error("expected error when writing to invalid dir, got nil")
	}
}

// TestWriteSummaryJSON_EmptySummary verifies JSON creation with zero-value summary.
func TestWriteSummaryJSON_EmptySummary(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSummaryJSON(dir, BatchSummary{})
	if err != nil {
		t.Fatalf("WriteSummaryJSON with empty summary: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected output file %s, got: %v", path, err)
	}
}

func TestWriteConfigYAML_WithAGCTargetRMS(t *testing.T) {
	dir := t.TempDir()
	cfg := TunedConfig{
		Comment:                  "test",
		VADThreshold:             0.25,
		AGCTargetRMS:             1500.0,
		JitterDepthFrames:        4,
		SuppressorAggressiveness: 2,
		Rationale:                map[string]string{"vad_threshold": "reason"},
	}
	path, err := WriteConfigYAML(dir, cfg)
	if err != nil {
		t.Fatalf("WriteConfigYAML with AGC: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "agc_target_rms") {
		t.Errorf("YAML missing agc_target_rms when AGCTargetRMS > 0: %s", data)
	}
}

func TestTuneFromBatchSummary_MediumSNRHighLatency(t *testing.T) {
	s := BatchSummary{
		AvgSNRImprovementDB: 5.0,  // 2 < 5 < 10 → medium (agg=2)
		AvgLatencyP95Ms:     10.0, // > 8ms → back off to 1
		AvgSpeechRatio:      0.6,
	}
	cfg := TuneFromBatchSummary(s)
	if cfg.SuppressorAggressiveness != 1 {
		t.Errorf("aggressiveness: want 1 (2 backed off for latency), got %d", cfg.SuppressorAggressiveness)
	}
}

func TestWriteCSV_WithErrorFile(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{
		{File: "good.wav", DurationMs: 1000},
		{File: "bad.wav", Error: "decode failed"},
	}
	path, err := WriteCSV(dir, files)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "decode failed") {
		t.Errorf("CSV should contain error field; got: %s", data)
	}
}

func TestWriteFilesJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteFilesJSON(dir, []FileResult{})
	if err != nil {
		t.Fatalf("WriteFilesJSON(empty): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected output %s: %v", path, err)
	}
}

func TestWriteFilesJSON_NonEmpty(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{
		{File: "test.wav", DurationMs: 2000, SampleRate: 16000, Channels: 1,
			SNR:     SNRResult{BeforeDB: 12, AfterDB: 22, ImprovementDB: 10},
			Latency: LatencyStats{MeanMs: 4.5, P95Ms: 7.2},
			VAD:     VADStats{SpeechRatio: 0.65, CPUSavedPct: 10.5}},
		{File: "err.wav", Error: "codec not found"},
	}
	path, err := WriteFilesJSON(dir, files)
	if err != nil {
		t.Fatalf("WriteFilesJSON: %v", err)
	}
	data, _ := os.ReadFile(path)
	var decoded []FileResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("want 2 entries, got %d", len(decoded))
	}
	if decoded[0].SNR.ImprovementDB != 10 {
		t.Errorf("ImprovementDB: want 10, got %.2f", decoded[0].SNR.ImprovementDB)
	}
	if decoded[1].Error != "codec not found" {
		t.Errorf("Error: want 'codec not found', got %q", decoded[1].Error)
	}
}

func TestWriteCSV_FullFields(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{{
		File: "full.wav", DurationMs: 3000, SampleRate: 16000, Channels: 1,
		SNR:     SNRResult{BeforeDB: 15.5, AfterDB: 25.5, ImprovementDB: 10.0},
		Latency: LatencyStats{MeanMs: 6.0, P95Ms: 9.0, RealTimeFactor: 0.6},
		VAD:     VADStats{SpeechRatio: 0.75, CPUSavedPct: 7.5},
		AGC:     AGCConvergence{TargetRMS: 3000, FramesToConverge: 40, ConvergedMs: 400, FinalRMS: 2950},
	}}
	path, err := WriteCSV(dir, files)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, _ := os.ReadFile(path)
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	idx := func(name string) int {
		for i, h := range rows[0] {
			if h == name {
				return i
			}
		}
		t.Fatalf("header %q not found", name)
		return -1
	}
	if rows[1][idx("snr_improvement_db")] != "10.000" {
		t.Errorf("snr_improvement_db: got %q", rows[1][idx("snr_improvement_db")])
	}
	if rows[1][idx("agc_target_rms")] != "3000.000" {
		t.Errorf("agc_target_rms: got %q", rows[1][idx("agc_target_rms")])
	}
}
