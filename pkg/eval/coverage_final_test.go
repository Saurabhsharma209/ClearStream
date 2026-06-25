package eval

// coverage_final_test.go -- targeted tests to push pkg/eval coverage past 80%.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAggregateResults_AllErrors verifies all-error results: FailedFiles increments, averages stay 0.
func TestAggregateResults_AllErrors(t *testing.T) {
	files := []FileResult{
		{File: "a.wav", Error: "decode error"},
		{File: "b.wav", Error: "missing file"},
	}
	s := AggregateResults(files, 1000)
	if s.TotalFiles != 2 {
		t.Errorf("TotalFiles: want 2, got %d", s.TotalFiles)
	}
	if s.FailedFiles != 2 {
		t.Errorf("FailedFiles: want 2, got %d", s.FailedFiles)
	}
	if s.ProcessedFiles != 0 {
		t.Errorf("ProcessedFiles: want 0, got %d", s.ProcessedFiles)
	}
	for _, v := range []float64{s.AvgSNRBeforeDB, s.AvgSNRAfterDB, s.AvgSNRImprovementDB, s.AvgLatencyMeanMs, s.AvgLatencyP95Ms, s.AvgSpeechRatio, s.AvgCPUSavedPct} {
		if v != 0 {
			t.Errorf("expected all averages 0 for all-error results: %+v", s)
			break
		}
	}
	if s.TotalDurationMs != 0 {
		t.Errorf("TotalDurationMs: want 0, got %.1f", s.TotalDurationMs)
	}
}

// TestAggregateResults_WallClockZero verifies SpeedRatio stays 0 when wallClockMs=0.
func TestAggregateResults_WallClockZero(t *testing.T) {
	files := []FileResult{
		{DurationMs: 500, SNR: SNRResult{BeforeDB: 10, AfterDB: 15, ImprovementDB: 5}},
	}
	s := AggregateResults(files, 0)
	if s.SpeedRatio != 0 {
		t.Errorf("SpeedRatio: want 0 for wallClockMs=0, got %.2f", s.SpeedRatio)
	}
	if s.TotalDurationMs != 500 {
		t.Errorf("TotalDurationMs: want 500, got %.1f", s.TotalDurationMs)
	}
}

// TestAggregateResults_SingleFile verifies correct aggregation for one file.
func TestAggregateResults_SingleFile(t *testing.T) {
	files := []FileResult{
		{
			File:       "solo.wav",
			DurationMs: 1000,
			SNR:        SNRResult{BeforeDB: 8.0, AfterDB: 18.0, ImprovementDB: 10.0},
			Latency:    LatencyStats{MeanMs: 3.0, P95Ms: 5.0},
			VAD:        VADStats{SpeechRatio: 0.7, CPUSavedPct: 9.0},
		},
	}
	s := AggregateResults(files, 200)
	if s.TotalFiles != 1 || s.ProcessedFiles != 1 || s.FailedFiles != 0 {
		t.Errorf("counts: total=%d processed=%d failed=%d", s.TotalFiles, s.ProcessedFiles, s.FailedFiles)
	}
	if math.Abs(s.AvgSNRBeforeDB-8.0) > 0.001 {
		t.Errorf("AvgSNRBeforeDB: want 8.0, got %.3f", s.AvgSNRBeforeDB)
	}
	if math.Abs(s.AvgSNRImprovementDB-10.0) > 0.001 {
		t.Errorf("AvgSNRImprovementDB: want 10.0, got %.3f", s.AvgSNRImprovementDB)
	}
	if math.Abs(s.SpeedRatio-5.0) > 0.001 {
		t.Errorf("SpeedRatio: want 5.0, got %.3f", s.SpeedRatio)
	}
}

// TestAggregateResults_EmptySlice verifies no panic for empty input.
func TestAggregateResults_EmptySlice(t *testing.T) {
	s := AggregateResults([]FileResult{}, 100)
	if s.TotalFiles != 0 {
		t.Errorf("TotalFiles: want 0, got %d", s.TotalFiles)
	}
}

// TestWriteFilesJSON_NonEmpty verifies WriteFilesJSON with a populated slice.
func TestWriteFilesJSON_NonEmpty(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{
		{
			File:       "test.wav",
			DurationMs: 2000,
			SampleRate: 16000,
			Channels:   1,
			SNR:        SNRResult{BeforeDB: 12.5, AfterDB: 22.5, ImprovementDB: 10.0},
			Latency:    LatencyStats{MeanMs: 2.0, P95Ms: 3.5, Samples: 200, MinMs: 0.8, MaxMs: 5.0, RealTimeFactor: 0.2},
			VAD:        VADStats{TotalFrames: 200, SpeechFrames: 150, SilenceFrames: 50, SpeechRatio: 0.75, CPUSavedPct: 7.5},
			AGC:        AGCConvergence{TargetRMS: 800, FramesToConverge: 10, ConvergedMs: 100, FinalRMS: 810},
		},
		{File: "broken.wav", Error: "unsupported codec"},
	}
	path, err := WriteFilesJSON(dir, files)
	if err != nil {
		t.Fatalf("WriteFilesJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var decoded []FileResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("len: want 2, got %d", len(decoded))
	}
	if decoded[0].File != "test.wav" {
		t.Errorf("File: want test.wav, got %s", decoded[0].File)
	}
	if math.Abs(decoded[0].SNR.ImprovementDB-10.0) > 0.001 {
		t.Errorf("SNR.ImprovementDB: want 10.0, got %.3f", decoded[0].SNR.ImprovementDB)
	}
	if decoded[0].AGC.FramesToConverge != 10 {
		t.Errorf("AGC.FramesToConverge: want 10, got %d", decoded[0].AGC.FramesToConverge)
	}
	if decoded[1].Error != "unsupported codec" {
		t.Errorf("Error: want unsupported codec, got %q", decoded[1].Error)
	}
}

// TestWriteCSV_FullFields verifies WriteCSV with all non-zero fields populated.
func TestWriteCSV_FullFields(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{
		{
			File:       "full.wav",
			DurationMs: 3000,
			SampleRate: 8000,
			Channels:   2,
			SNR:        SNRResult{BeforeDB: 9.1, AfterDB: 19.5, ImprovementDB: 10.4},
			Latency:    LatencyStats{MeanMs: 3.5, P95Ms: 6.2, Samples: 300, MinMs: 1.0, MaxMs: 9.0, RealTimeFactor: 0.35},
			VAD:        VADStats{TotalFrames: 300, SpeechFrames: 210, SilenceFrames: 90, SpeechRatio: 0.7, CPUSavedPct: 9.0},
			AGC:        AGCConvergence{TargetRMS: 1200, FramesToConverge: 15, ConvergedMs: 150, FinalRMS: 1180},
		},
	}
	path, err := WriteCSV(dir, files)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open CSV: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV rows: want 2, got %d", len(rows))
	}
	row := rows[1]
	if row[0] != "full.wav" {
		t.Errorf("file: want full.wav, got %s", row[0])
	}
	if row[3] != "2" {
		t.Errorf("channels: want 2, got %s", row[3])
	}
	snrBefore, _ := strconv.ParseFloat(row[4], 64)
	if math.Abs(snrBefore-9.1) > 0.001 {
		t.Errorf("snr_before_db: want 9.1, got %.3f", snrBefore)
	}
	agcTarget, _ := strconv.ParseFloat(row[14], 64)
	if math.Abs(agcTarget-1200) > 0.1 {
		t.Errorf("agc_target_rms: want 1200, got %.1f", agcTarget)
	}
	if row[15] != "15" {
		t.Errorf("agc_frames_to_converge: want 15, got %s", row[15])
	}
	if row[18] != "" {
		t.Errorf("error col: want empty, got %q", row[18])
	}
}

// TestWriteCSV_MultipleRowsWithError verifies multiple rows including error field.
func TestWriteCSV_MultipleRowsWithError(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{
		{
			File: "a.wav", DurationMs: 1000, SampleRate: 16000, Channels: 1,
			SNR:     SNRResult{BeforeDB: 5.0, AfterDB: 15.0, ImprovementDB: 10.0},
			Latency: LatencyStats{MeanMs: 1.5, P95Ms: 2.5, RealTimeFactor: 0.15},
			VAD:     VADStats{SpeechRatio: 0.6, CPUSavedPct: 12.0},
			AGC:     AGCConvergence{TargetRMS: 600, FramesToConverge: 5, ConvergedMs: 50, FinalRMS: 590},
		},
		{File: "b.wav", Error: "timeout"},
	}
	path, err := WriteCSV(dir, files)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "a.wav") {
		t.Error("CSV missing a.wav")
	}
	if !strings.Contains(content, "timeout") {
		t.Error("CSV missing error field timeout")
	}
}

// TestMax verifies the max helper for both branches.
func TestMax(t *testing.T) {
	if got := max(5, 3); got != 5 {
		t.Errorf("max(5,3): want 5, got %d", got)
	}
	if got := max(3, 5); got != 5 {
		t.Errorf("max(3,5): want 5, got %d", got)
	}
	if got := max(4, 4); got != 4 {
		t.Errorf("max(4,4): want 4, got %d", got)
	}
}

// TestBatchRunner_Run_InvalidOutputDir verifies Run returns error for unwritable output dir.
func TestBatchRunner_Run_InvalidOutputDir(t *testing.T) {
	sup := &passthroughSuppressor{}
	r := NewBatchRunner(BatchConfig{
		InputDir:   t.TempDir(),
		OutputDir:  "/dev/null/impossible/path",
		Suppressor: sup,
	})
	_, err := r.Run(context.Background())
	if err == nil {
		t.Error("expected error for unwritable output dir, got nil")
	}
}

// TestBatchRunner_Run_EmptyInputDir verifies Run succeeds with empty summary for no audio files.
func TestBatchRunner_Run_EmptyInputDir(t *testing.T) {
	sup := &passthroughSuppressor{}
	inputDir := t.TempDir()
	os.WriteFile(inputDir+"/readme.txt", []byte("hello"), 0o644)
	r := NewBatchRunner(BatchConfig{
		InputDir:   inputDir,
		OutputDir:  t.TempDir(),
		Suppressor: sup,
	})
	summary, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.TotalFiles != 0 {
		t.Errorf("TotalFiles: want 0, got %d", summary.TotalFiles)
	}
}

// TestBatchRunner_Run_ContextCancelledBeforeStart verifies pre-cancelled context
// causes Run to return quickly without processing files.
func TestBatchRunner_Run_ContextCancelledBeforeStart(t *testing.T) {
	sup := &passthroughSuppressor{}
	inputDir := t.TempDir()
	os.WriteFile(inputDir+"/test.wav", []byte("not-real-wav"), 0o644)
	r := NewBatchRunner(BatchConfig{
		InputDir:   inputDir,
		OutputDir:  t.TempDir(),
		Suppressor: sup,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s after context cancel")
	}
}

// TestDecodeToRawPCM_BadFFmpegPath verifies error for non-existent ffmpeg binary.
func TestDecodeToRawPCM_BadFFmpegPath(t *testing.T) {
	ctx := context.Background()
	_, _, _, err := decodeToRawPCM(ctx, "/nonexistent/ffmpeg", "/tmp/test.wav", 16000)
	if err == nil {
		t.Error("expected error for non-existent ffmpeg, got nil")
	}
}

// TestDecodeToRawPCM_CancelledContext verifies that a cancelled context returns an error.
func TestDecodeToRawPCM_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := decodeToRawPCM(ctx, "ffmpeg", "/tmp/nonexistent.wav", 16000)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}
