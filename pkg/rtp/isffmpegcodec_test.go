package rtp

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
)

// TestIsFFmpegCodec pins down the FFmpeg-subprocess/cheap-in-process split
// that handlePacket's bypass fast path relies on to decide whether priming
// the jitter buffer's PLC source (by decoding one frame) is worth the cost
// while suppression is bypassed. isFFmpegCodec sat at 66.7% coverage --
// exercised indirectly by other pkg/rtp tests for a subset of codecs, but
// never asserted on directly for its full decision table. If a future codec
// is added to audio.Codec without updating this switch, it silently falls
// into the "cheap" default -- this test is the regression guard for that.
func TestIsFFmpegCodec(t *testing.T) {
	tests := []struct {
		codec audio.Codec
		want  bool
	}{
		{audio.CodecOpus, true},
		{audio.CodecG722, true},
		{audio.CodecG729, true},
		{audio.CodecG711U, false},
		{audio.CodecG711A, false},
		{audio.CodecPCM, false},
		{audio.CodecUnknown, false},
		{audio.Codec("some-future-codec"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.codec), func(t *testing.T) {
			if got := isFFmpegCodec(tt.codec); got != tt.want {
				t.Errorf("isFFmpegCodec(%q) = %v, want %v", tt.codec, got, tt.want)
			}
		})
	}
}
