package main

import (
	"testing"
	"time"
)

func TestParseBatchArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, ba batchArgs)
	}{
		{
			name:    "missing input-dir",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "input-dir required even with other flags set",
			args:    []string{"--workers", "4"},
			wantErr: true,
		},
		{
			name: "valid minimal",
			args: []string{"--input-dir", "/tmp/recordings"},
			check: func(t *testing.T, ba batchArgs) {
				if ba.InputDir != "/tmp/recordings" {
					t.Errorf("InputDir = %q, want /tmp/recordings", ba.InputDir)
				}
				if ba.OutputDir != "eval-out" {
					t.Errorf("OutputDir = %q, want default eval-out", ba.OutputDir)
				}
				if ba.UseAGC {
					t.Errorf("UseAGC = true, want default false")
				}
			},
		},
		{
			name: "valid full",
			args: []string{"--input-dir", "/tmp/recordings", "--output-dir", "/tmp/out", "--workers", "3", "--agc"},
			check: func(t *testing.T, ba batchArgs) {
				if ba.InputDir != "/tmp/recordings" || ba.OutputDir != "/tmp/out" || ba.Workers != 3 || !ba.UseAGC {
					t.Errorf("unexpected result: %+v", ba)
				}
			},
		},
		{
			name:    "unknown flag",
			args:    []string{"--input-dir", "/tmp/x", "--bogus", "1"},
			wantErr: true,
		},
		{
			name:    "malformed workers value",
			args:    []string{"--input-dir", "/tmp/x", "--workers", "notanumber"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ba, err := parseBatchArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseBatchArgs(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, ba)
			}
		})
	}
}

func TestParseRTPArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, ra rtpArgs)
	}{
		{
			name: "defaults",
			args: []string{},
			check: func(t *testing.T, ra rtpArgs) {
				if ra.OutputDir != "eval-out" {
					t.Errorf("OutputDir = %q, want eval-out", ra.OutputDir)
				}
				if ra.Interval != time.Second {
					t.Errorf("Interval = %v, want 1s", ra.Interval)
				}
				if ra.Duration != 0 {
					t.Errorf("Duration = %v, want 0 (run until Ctrl-C)", ra.Duration)
				}
			},
		},
		{
			name: "valid duration and interval",
			args: []string{"--duration", "30s", "--interval", "500ms", "--output-dir", "/tmp/out"},
			check: func(t *testing.T, ra rtpArgs) {
				if ra.Duration != 30*time.Second {
					t.Errorf("Duration = %v, want 30s", ra.Duration)
				}
				if ra.Interval != 500*time.Millisecond {
					t.Errorf("Interval = %v, want 500ms", ra.Interval)
				}
				if ra.OutputDir != "/tmp/out" {
					t.Errorf("OutputDir = %q, want /tmp/out", ra.OutputDir)
				}
			},
		},
		{
			name:    "invalid interval string",
			args:    []string{"--interval", "notaduration"},
			wantErr: true,
		},
		{
			name:    "invalid duration string",
			args:    []string{"--duration", "notaduration"},
			wantErr: true,
		},
		{
			name:    "zero interval rejected",
			args:    []string{"--interval", "0s"},
			wantErr: true,
		},
		{
			name:    "negative interval rejected",
			args:    []string{"--interval", "-1s"},
			wantErr: true,
		},
		{
			name:    "zero duration rejected",
			args:    []string{"--duration", "0s"},
			wantErr: true,
		},
		{
			name:    "negative duration rejected",
			args:    []string{"--duration", "-5s"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"--bogus", "1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ra, err := parseRTPArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRTPArgs(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, ra)
			}
		})
	}
}
