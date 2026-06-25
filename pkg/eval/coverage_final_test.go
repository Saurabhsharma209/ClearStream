package eval

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestAggregateResults_AllErrors(t *testing.T) {
	files := []FileResult{
		{File: "a.wav", Error: "ffmpeg failed"},
		{File: "b.wav", Error: "not found"},
	}
	s := AggregateResults(files, 100)
	if s.TotalFiles != 2 {
		t.Errorf("TotalFiles: want 2, got %d", s.TotalFiles)
	}
	if s.FailedFiles != 2 {
		t.Errorf("FailedFiles: want 2, got %d", s.FailedFiles)
	}
	if s.ProcessedFiles != 0 {
		t.Errorf("ProcessedFiles: want 0, got %d", s.ProcessedFiles)
	}
	if s.AvgSNRBeforeDB != 0 || s.AvgSNRAfterDB != 0 || s.AvgLatencyMeanMs != 0 {
		t.Errorf("averages must be 0 when all files error")
	}
}

func TestAggregateResults_WallClockZero(t *testing.T) {
	files := []FileResult{{File: "a.wav", DurationMs: 500}}
	s := AggregateResults(files, 0)
	if s.SpeedRatio != 0 {
		t.Errorf("SpeedRatio: want 0 when wallClockMs=0, got %.4f", s.SpeedRatio)
	}
}

func TestAggregateResults_EmptySlice(t *testing.T) {
	s := AggregateResults(nil, 0)
	if s.TotalFiles != 0 || s.ProcessedFiles != 0 || s.FailedFiles != 0 {
		t.Errorf("empty slice: unexpected counts")
	}
}

func TestAggregateResults_SingleFile(t *testing.T) {
	files := []FileResult{{
		File:       "c.wav",
		DurationMs: 1000,
		SNR:        SNRResult{BeforeDB: 10, AfterDB: 20, ImprovementDB: 10},
		Latency:    LatencyStats{MeanMs: 3.0, P95Ms: 5.0},
		VAD:        VADStats{SpeechRatio: 0.7, CPUSavedPct: 9.0},
	}}
	s := AggregateResults(files, 200)
	if s.ProcessedFiles != 1 {
		t.Fatalf("ProcessedFiles: want 1, got %d", s.ProcessedFiles)
	}
	if s.AvgSNRImprovementDB != 10 {
		t.Errorf("AvgSNRImprovementDB: want 10, got %.2f", s.AvgSNRImprovementDB)
	}
	if s.SpeedRatio != 5.0 {
		t.Errorf("SpeedRatio: want 5.0, got %.4f", s.SpeedRatio)
	}
}

func TestAggregateResults_MixedErrorAndSuccess(t *testing.T) {
	files := []FileResult{
		{File: "ok.wav", DurationMs: 500, SNR: SNRResult{BeforeDB: 8, AfterDB: 18, ImprovementDB: 10}},
		{File: "bad.wav", Error: "timeout"},
	}
	s := AggregateResults(files, 100)
	if s.ProcessedFiles != 1 || s.FailedFiles != 1 {
		t.Errorf("want 1 processed 1 failed, got %d/%d", s.ProcessedFiles, s.FailedFiles)
	}
	if s.AvgSNRImprovementDB != 10 {
		t.Errorf("AvgSNRImprovementDB: want 10, got %.2f", s.AvgSNRImprovementDB)
	}
}

func TestEstimateSNRFromLoss_Zero(t *testing.T) {
	if got := estimateSNRFromLoss(0); got != 30.0 {
		t.Errorf("lossPct=0: want 30.0, got %.2f", got)
	}
}

func TestEstimateSNRFromLoss_Negative(t *testing.T) {
	if got := estimateSNRFromLoss(-1); got != 30.0 {
		t.Errorf("lossPct=-1: want 30.0, got %.2f", got)
	}
}

func TestEstimateSNRFromLoss_High(t *testing.T) {
	if got := estimateSNRFromLoss(10); got != 0 {
		t.Errorf("lossPct=10: want 0 (clamped), got %.2f", got)
	}
}

func TestEstimateSNRFromLoss_Mid(t *testing.T) {
	if got := estimateSNRFromLoss(5); got != 10.0 {
		t.Errorf("lossPct=5: want 10.0, got %.2f", got)
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
