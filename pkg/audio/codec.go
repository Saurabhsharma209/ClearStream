// Package audio provides low-level audio codec detection, decoding,
// resampling, and encoding via FFmpeg.
package audio

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Codec represents a known audio codec.
type Codec string

const (
	CodecPCM     Codec = "pcm_s16le" // raw signed 16-bit PCM
	CodecOpus    Codec = "opus"
	CodecG711U   Codec = "pcm_mulaw" // G.711 µ-law (PCMU)
	CodecG711A   Codec = "pcm_alaw"  // G.711 A-law (PCMA)
	CodecG722    Codec = "g722"
	CodecG729    Codec = "g729"
	CodecAAC     Codec = "aac"
	CodecMP3     Codec = "mp3"
	CodecFLAC    Codec = "flac"
	CodecVorbis  Codec = "vorbis"
	CodecSpeex   Codec = "speex"
	CodecGSM     Codec = "gsm"
	CodecILBC    Codec = "ilbc"
	CodecUnknown Codec = "unknown"
)

// codecSampleRates maps codecs to their native sample rates.
// The AI model always gets 16kHz PCM; these are used for re-encoding.
var codecSampleRates = map[Codec]int{
	CodecPCM:    16000,
	CodecOpus:   48000,
	CodecG711U:  8000,
	CodecG711A:  8000,
	CodecG722:   16000,
	CodecG729:   8000,
	CodecAAC:    44100,
	CodecMP3:    44100,
	CodecFLAC:   44100,
	CodecVorbis: 44100,
	CodecSpeex:  16000,
	CodecGSM:    8000,
	CodecILBC:   8000,
}

// NativeSampleRate returns the standard sample rate for a codec.
func (c Codec) NativeSampleRate() int {
	if r, ok := codecSampleRates[c]; ok {
		return r
	}
	return 8000
}

// IsLossless reports whether the codec is lossless.
func (c Codec) IsLossless() bool {
	return c == CodecPCM || c == CodecFLAC
}

// MediaInfo holds detected metadata about a media file.
type MediaInfo struct {
	// HasVideo is true when the file contains a video stream.
	HasVideo bool

	// AudioCodec is the detected audio codec.
	AudioCodec Codec

	// VideoCodec is the detected video codec (empty if HasVideo is false).
	VideoCodec string

	// SampleRate is the original audio sample rate.
	SampleRate int

	// Channels is the number of audio channels.
	Channels int

	// DurationSec is the file duration in seconds.
	DurationSec float64

	// BitRate is the audio stream bitrate in kbps.
	BitRate int

	// ContainerFormat is the detected container (e.g. "mp4", "wav").
	ContainerFormat string
}

// Probe uses ffprobe to detect codec and stream info from a media file.
func Probe(ffmpegPath, filePath string) (*MediaInfo, error) {
	probePath := strings.Replace(ffmpegPath, "ffmpeg", "ffprobe", 1)

	out, err := exec.Command(probePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		filePath,
	).Output()
	if err != nil {
		// Fallback: use ffmpeg -i and parse stderr
		return probeViaFFmpeg(ffmpegPath, filePath)
	}

	return parseFFprobeJSON(out, filePath)
}

// probeViaFFmpeg is a fallback probe using ffmpeg -i output (stderr).
func probeViaFFmpeg(ffmpegPath, filePath string) (*MediaInfo, error) {
	cmd := exec.Command(ffmpegPath, "-i", filePath)
	// ffmpeg -i always exits non-zero without output args; we want stderr
	stderr, _ := cmd.CombinedOutput()
	return parseFFmpegInfo(string(stderr), filePath)
}

// parseFFprobeJSON parses ffprobe JSON output into MediaInfo.
func parseFFprobeJSON(data []byte, filePath string) (*MediaInfo, error) {
	// Lightweight manual parse to avoid json import cycles in this layer.
	// For production, use encoding/json.
	info := &MediaInfo{
		ContainerFormat: strings.TrimPrefix(filepath.Ext(filePath), "."),
	}

	s := string(data)

	// Detect video stream
	if strings.Contains(s, `"codec_type": "video"`) {
		info.HasVideo = true
		info.VideoCodec = extractJSONField(s, "codec_name", "video")
	}

	// Detect audio codec
	audioCodecStr := extractJSONField(s, "codec_name", "audio")
	info.AudioCodec = normalizeCodec(audioCodecStr)

	// Sample rate
	srStr := extractJSONField(s, "sample_rate", "audio")
	fmt.Sscanf(srStr, "%d", &info.SampleRate)
	if info.SampleRate == 0 {
		info.SampleRate = info.AudioCodec.NativeSampleRate()
	}

	// Channels
	fmt.Sscanf(extractJSONField(s, "channels", "audio"), "%d", &info.Channels)
	if info.Channels == 0 {
		info.Channels = 1
	}

	// Duration
	fmt.Sscanf(extractJSONField(s, "duration", ""), "%f", &info.DurationSec)

	return info, nil
}

// parseFFmpegInfo parses the stderr output of `ffmpeg -i` as fallback.
func parseFFmpegInfo(stderr, filePath string) (*MediaInfo, error) {
	info := &MediaInfo{
		ContainerFormat: strings.TrimPrefix(filepath.Ext(filePath), "."),
		SampleRate:      8000,
		Channels:        1,
	}

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "Video:") {
			info.HasVideo = true
			parts := strings.Split(line, "Video: ")
			if len(parts) > 1 {
				info.VideoCodec = strings.Fields(parts[1])[0]
			}
		}

		if strings.Contains(line, "Audio:") {
			parts := strings.Split(line, "Audio: ")
			if len(parts) > 1 {
				fields := strings.Fields(parts[1])
				if len(fields) > 0 {
					info.AudioCodec = normalizeCodec(strings.TrimRight(fields[0], ","))
				}
			}
			// Parse sample rate: "44100 Hz"
			if idx := strings.Index(line, " Hz"); idx > 0 {
				rateStr := strings.Fields(line[:idx])
				if len(rateStr) > 0 {
					fmt.Sscanf(rateStr[len(rateStr)-1], "%d", &info.SampleRate)
				}
			}
			// Parse channels: "stereo" or "mono" or "2 channels"
			if strings.Contains(line, "stereo") {
				info.Channels = 2
			} else if strings.Contains(line, "mono") {
				info.Channels = 1
			}
		}

		if strings.Contains(line, "Duration:") {
			var h, m, s int
			var ms int
			fmt.Sscanf(line, "Duration: %d:%d:%d.%d", &h, &m, &s, &ms)
			info.DurationSec = float64(h*3600+m*60+s) + float64(ms)/100.0
		}
	}

	return info, nil
}

// normalizeCodec maps ffmpeg codec name strings to Codec constants.
func normalizeCodec(name string) Codec {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "pcm_s16le", name == "pcm_s16be", name == "pcm_u8":
		return CodecPCM
	case name == "opus":
		return CodecOpus
	case name == "pcm_mulaw", name == "mulaw", name == "ulaw":
		return CodecG711U
	case name == "pcm_alaw", name == "alaw":
		return CodecG711A
	case name == "g722":
		return CodecG722
	case name == "g729":
		return CodecG729
	case name == "aac":
		return CodecAAC
	case name == "mp3", name == "libmp3lame":
		return CodecMP3
	case name == "flac":
		return CodecFLAC
	case name == "vorbis", name == "libvorbis":
		return CodecVorbis
	case name == "speex", name == "libspeex":
		return CodecSpeex
	case name == "gsm":
		return CodecGSM
	case name == "ilbc", name == "libilbc":
		return CodecILBC
	default:
		return CodecUnknown
	}
}

// extractJSONField is a minimal field extractor for ffprobe JSON output.
// It finds the first occurrence of `"key": "value"` near the streamType context.
func extractJSONField(s, key, streamType string) string {
	target := fmt.Sprintf(`"%s": "`, key)
	searchIn := s
	if streamType != "" {
		// Find the block containing the stream type
		marker := fmt.Sprintf(`"codec_type": "%s"`, streamType)
		if idx := strings.Index(s, marker); idx >= 0 {
			// Look in a window around the marker
			start := idx - 500
			if start < 0 {
				start = 0
			}
			end := idx + 500
			if end > len(s) {
				end = len(s)
			}
			searchIn = s[start:end]
		}
	}
	idx := strings.Index(searchIn, target)
	if idx < 0 {
		return ""
	}
	rest := searchIn[idx+len(target):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
