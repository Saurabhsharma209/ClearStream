package eval

import "testing"

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
