package rtp

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
)

// PlaybackQueue is a bounded FIFO queue of outbound RTP audio frames.
// It is written by the WSS receiver goroutine and read by the RTP sender goroutine.
// On barge-in (caller interruption), Clear() empties the queue immediately.
//
// Concurrency: safe for concurrent Push/Pop/Clear from different goroutines.
type PlaybackQueue struct {
	mu       sync.Mutex
	frames   [][]byte // each frame is a G.711 encoded packet payload
	maxDepth int
	dropped  uint64 // frames dropped due to full queue (accessed via sync/atomic)
	pushed   uint64
	popped   uint64
	cleared  uint64
}

// NewPlaybackQueue creates a queue with the given maximum depth.
// A depth of 50 at 20ms/frame = 1 second of audio — reasonable for bot TTS.
func NewPlaybackQueue(maxDepth int) *PlaybackQueue {
	if maxDepth <= 0 {
		maxDepth = 50
	}
	return &PlaybackQueue{
		frames:   make([][]byte, 0, maxDepth),
		maxDepth: maxDepth,
	}
}

// Push adds an encoded audio frame to the queue.
// If the queue is full, the frame is dropped and the drop counter increments.
// Thread-safe.
func (q *PlaybackQueue) Push(frame []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.frames) >= q.maxDepth {
		atomic.AddUint64(&q.dropped, 1)
		return false
	}
	cp := make([]byte, len(frame))
	copy(cp, frame)
	q.frames = append(q.frames, cp)
	atomic.AddUint64(&q.pushed, 1)
	return true
}

// Pop removes and returns the next frame, or nil if the queue is empty.
// Thread-safe.
func (q *PlaybackQueue) Pop() []byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.frames) == 0 {
		return nil
	}
	frame := q.frames[0]
	q.frames = q.frames[1:]
	atomic.AddUint64(&q.popped, 1)
	return frame
}

// Clear empties the queue immediately (called on barge-in).
// Returns the number of frames discarded.
// Thread-safe.
func (q *PlaybackQueue) Clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.frames)
	q.frames = q.frames[:0]
	atomic.AddUint64(&q.cleared, uint64(n))
	return n
}

// Len returns the current queue depth. Thread-safe.
func (q *PlaybackQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.frames)
}

// PlaybackStats holds playback queue counters.
type PlaybackStats struct {
	Pushed  uint64
	Popped  uint64
	Dropped uint64
	Cleared uint64
}

// Stats returns a snapshot of queue counters.
func (q *PlaybackQueue) Stats() PlaybackStats {
	return PlaybackStats{
		Pushed:  atomic.LoadUint64(&q.pushed),
		Popped:  atomic.LoadUint64(&q.popped),
		Dropped: atomic.LoadUint64(&q.dropped),
		Cleared: atomic.LoadUint64(&q.cleared),
	}
}

// startPlaybackLoop is a goroutine that pops frames from the playback queue every
// 20ms and sends them to fwdAddr as RTP packets. It exits when ctx is cancelled.
func (s *Session) startPlaybackLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	// Use a random SSRC distinct from the inbound stream.
	ssrc := rand.Uint32()
	var seq uint16
	var ts uint32
	const tsStep uint32 = 160 // 8kHz × 20ms

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame := s.playback.Pop()
			if frame == nil {
				// Nothing to send this tick — advance timestamp to stay in sync.
				ts += tsStep
				continue
			}

			h := rtpHeader{
				Version:        2,
				PayloadType:    s.cfg.PayloadType,
				SequenceNumber: seq,
				Timestamp:      ts,
				SSRC:           ssrc,
			}
			pkt := buildRTPPacket(h, frame)
			seq++
			ts += tsStep

			if _, err := s.conn.WriteToUDP(pkt, s.fwdAddr); err != nil {
				// Conn closed — exit silently.
				return
			}
		}
	}
}

// InjectBotAudio takes 8kHz mono PCM16 samples (little-endian) from a bot/TTS
// source, encodes them to the session codec, and pushes each 160-sample (20ms
// @ 8kHz) frame into the playback queue. Returns true if all frames were
// accepted.
//
// Equivalent to InjectBotAudioAtRate(pcm16, 8000). Use InjectBotAudioAtRate
// directly for 16kHz (or other) source audio -- see that method doc for why
// this one no longer claims to accept 16kHz input directly.
func (s *Session) InjectBotAudio(pcm16 []byte) bool {
	return s.InjectBotAudioAtRate(pcm16, 8000)
}

// InjectBotAudioAtRate takes mono PCM16 samples (little-endian) at sampleRate
// from a bot/TTS source, resamples to 8kHz if sampleRate != 8000, encodes the
// result to the session codec, and pushes each 160-sample (20ms @ 8kHz) frame
// into the playback queue. Returns true if all frames were accepted.
//
// Fixes a real bug in the previous single-rate InjectBotAudio: its doc
// comment promised 8kHz-or-16kHz mono input, but the implementation always
// chunked samples into 160-sample (20ms @ 8kHz) blocks and fed them straight
// to the 8kHz G.711 encoder with no resampling step at all -- 16kHz bot/TTS
// audio (a common TTS output rate) played back at 2x speed with doubled
// pitch, not merely lower quality. 16kHz (or any other non-8kHz rate) is now
// downsampled to 8kHz via pkg/audio.Resample anti-alias-filtered path (the
// same Kaiser-FIR downsampler pkg/rtp/session.go already uses for inbound
// G.711 upsampling) before encoding.
func (s *Session) InjectBotAudioAtRate(pcm16 []byte, sampleRate int) bool {
	samples := bytesToInt16Slice(pcm16)

	if sampleRate <= 0 {
		sampleRate = 8000
	}
	if sampleRate != 8000 {
		if resampled, err := audio.Resample(samples, sampleRate, 8000); err == nil {
			samples = resampled
		}
	}

	// Encode using the session codec, defaulting to G.711 µ-law.
	codec := s.cfg.Codec
	if codec == "" || codec == audio.CodecUnknown {
		codec = audio.CodecG711U
	}

	const frameSize = 160 // 160 samples = 20ms @ 8kHz

	allAccepted := true
	for i := 0; i+frameSize <= len(samples); i += frameSize {
		chunk := samples[i : i+frameSize]
		var encoded []byte
		switch codec {
		case audio.CodecG711A:
			encoded = encodeG711A(chunk)
		default:
			encoded = encodeG711U(chunk)
		}
		if !s.playback.Push(encoded) {
			allAccepted = false
		}
	}

	// Handle a partial final frame (pad with silence).
	remainder := len(samples) % frameSize
	if remainder > 0 {
		chunk := make([]int16, frameSize)
		copy(chunk, samples[len(samples)-remainder:])
		var encoded []byte
		switch codec {
		case audio.CodecG711A:
			encoded = encodeG711A(chunk)
		default:
			encoded = encodeG711U(chunk)
		}
		if !s.playback.Push(encoded) {
			allAccepted = false
		}
	}

	return allAccepted
}

// ClearPlayback discards all queued bot audio frames (called on barge-in).
// Returns the number of frames discarded.
func (s *Session) ClearPlayback() int {
	return s.playback.Clear()
}

// PlaybackStats returns a snapshot of playback queue counters.
func (s *Session) PlaybackStats() PlaybackStats {
	return s.playback.Stats()
}
