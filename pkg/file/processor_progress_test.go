package file

import "testing"

// TestParseFFmpegProgressLine covers parseFFmpegProgressLine, including the
// truncated/malformed "time=" case that previously panicked with an
// index-out-of-range on strings.Fields(...)[0] when "time=" was the last
// token on a line with nothing following it (e.g. a partial final stderr
// line flushed just before ffmpeg exits on cancellation/kill).
func TestParseFFmpegProgressLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		durSec     float64
		wantOK     bool
		wantPctMin float64
		wantPctMax float64
	}{
		{
			name:       "normal progress line midway through",
			line:       "frame=  100 fps=25 q=-1.0 size=     256kB time=00:00:05.00 bitrate= 419.4kbits/s speed=1x",
			durSec:     10.0,
			wantOK:     true,
			wantPctMin: 0.39, // 0.1 + (5/10)*0.6 = 0.4
			wantPctMax: 0.41,
		},
		{
			name:   "truncated line: time= is the last token with nothing after it",
			line:   "frame=  100 fps=25 q=-1.0 size=     256kB time=",
			durSec: 10.0,
			wantOK: false, // previously panicked here (strings.Fields("")[0])
		},
		{
			name:   "time= followed only by trailing whitespace",
			line:   "frame=  100 fps=25 time=   ",
			durSec: 10.0,
			wantOK: false,
		},
		{
			name:   "no time= substring at all",
			line:   "frame=  100 fps=25 q=-1.0 size=     256kB bitrate= 419.4kbits/s",
			durSec: 10.0,
			wantOK: false,
		},
		{
			name:   "unparseable time value (N/A)",
			line:   "frame=  100 time=N/A bitrate=N/A",
			durSec: 10.0,
			wantOK: false,
		},
		{
			name:   "totalDurationSec is zero",
			line:   "time=00:00:05.00",
			durSec: 0,
			wantOK: false,
		},
		{
			name:   "totalDurationSec is negative",
			line:   "time=00:00:05.00",
			durSec: -1,
			wantOK: false,
		},
		{
			name:       "progress caps at 0.69 near end of stream",
			line:       "time=00:01:00.00",
			durSec:     10.0, // secs (60) far exceeds duration -> pct would exceed 0.69
			wantOK:     true,
			wantPctMin: 0.69,
			wantPctMax: 0.69,
		},
		{
			name:   "progress at very start",
			line:   "time=00:00:00.00",
			durSec: 10.0,
			wantOK: false, // secs == 0 -> treated as no usable progress (secs <= 0 check)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, ok := parseFFmpegProgressLine(tt.line, tt.durSec)
			if ok != tt.wantOK {
				t.Fatalf("parseFFmpegProgressLine(%q, %v) ok = %v, want %v (pct=%v)", tt.line, tt.durSec, ok, tt.wantOK, pct)
			}
			if ok {
				if pct < tt.wantPctMin || pct > tt.wantPctMax {
					t.Errorf("parseFFmpegProgressLine(%q, %v) pct = %v, want in [%v, %v]", tt.line, tt.durSec, pct, tt.wantPctMin, tt.wantPctMax)
				}
			}
		})
	}
}

// TestParseFFmpegProgressLineNoPanicOnTruncatedInput is a focused regression
// test: calling parseFFmpegProgressLine with a line where "time=" is the
// final content (no trailing token) must never panic. This is the exact
// shape of a real FFmpeg stderr stream truncated mid-write when the process
// is killed/cancelled before a full progress line is flushed.
func TestParseFFmpegProgressLineNoPanicOnTruncatedInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parseFFmpegProgressLine panicked on truncated input: %v", r)
		}
	}()
	for _, line := range []string{
		"time=",
		"foo bar time=",
		"time=   ",
		"a b c time=\t",
	} {
		if _, ok := parseFFmpegProgressLine(line, 10.0); ok {
			t.Errorf("expected ok=false for truncated line %q", line)
		}
	}
}
