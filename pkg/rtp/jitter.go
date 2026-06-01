// Package rtp provides real-time RTP stream interception and noise suppression.
package rtp

import (
	"math"
	"sort"
	"sync"
	"time"
)

const (
	defaultJitterDepth    = 4    // number of frames to buffer
	defaultJitterMaxDepth = 32   // maximum buffer depth before dropping
	maxSeqDrift           = 1000 // sequence number drift before reset
)

// jitterEntry holds a buffered RTP packet.
type jitterEntry struct {
	seq       uint16
	timestamp uint32
	payload   []byte
	received  time.Time
}

// JitterBuffer smooths out packet arrival variance for real-time audio.
// It reorders out-of-order packets and conceals loss.
type JitterBuffer struct {
	mu              sync.Mutex
	buf             []jitterEntry
	nextSeq         uint16
	depth           int // target buffer depth (frames)
	maxDepth        int
	primed          bool
	consecutiveLoss int     // count of consecutive lost frames
	lastGoodFrame   []int16 // last successfully received frame (decoded PCM)
}

// NewJitterBuffer creates a jitter buffer with the given target depth.
func NewJitterBuffer(depth int) *JitterBuffer {
	if depth <= 0 {
		depth = defaultJitterDepth
	}
	return &JitterBuffer{
		depth:    depth,
		maxDepth: defaultJitterMaxDepth,
		buf:      make([]jitterEntry, 0, depth*2),
	}
}

// Push inserts an incoming RTP packet into the buffer.
// Returns true if the buffer has accumulated enough packets to start draining.
func (j *JitterBuffer) Push(seq uint16, ts uint32, payload []byte) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Copy payload
	p := make([]byte, len(payload))
	copy(p, payload)

	j.buf = append(j.buf, jitterEntry{
		seq:       seq,
		timestamp: ts,
		payload:   p,
		received:  time.Now(),
	})

	// Sort by sequence number
	sort.Slice(j.buf, func(i, k int) bool {
		return seqLess(j.buf[i].seq, j.buf[k].seq)
	})

	// Drop oldest if buffer overflows
	for len(j.buf) > j.maxDepth {
		j.buf = j.buf[1:]
	}

	if !j.primed && len(j.buf) >= j.depth {
		j.primed = true
		j.nextSeq = j.buf[0].seq
	}

	return j.primed
}

// Pop returns the next expected packet payload.
// If the packet is missing (loss), returns a nil payload (caller should conceal).
// Returns (nil, false) when the buffer is not yet primed or is empty.
func (j *JitterBuffer) Pop() ([]byte, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.primed || len(j.buf) == 0 {
		return nil, false
	}

	// Check if the head matches the expected sequence
	head := j.buf[0]
	if head.seq == j.nextSeq {
		j.buf = j.buf[1:]
		j.nextSeq++
		return head.payload, true
	}

	// Packet lost or reordered beyond tolerance — conceal
	j.nextSeq++
	return nil, true // nil payload = packet loss; caller should repeat last good frame
}

// generatePLC produces a packet-loss-concealment frame using fade-to-silence.
// Each consecutive lost frame is multiplied by 0.9 so audio decays gracefully
// rather than looping the last good frame indefinitely.
// Must be called with j.mu held OR from a single-threaded context.
func (j *JitterBuffer) generatePLC() []int16 {
	j.consecutiveLoss++
	if j.lastGoodFrame == nil {
		return make([]int16, 160) // silence
	}
	decayFactor := math.Pow(0.9, float64(j.consecutiveLoss))
	result := make([]int16, len(j.lastGoodFrame))
	for i, s := range j.lastGoodFrame {
		result[i] = int16(float64(s) * decayFactor)
	}
	return result
}

// onGoodPacket resets the consecutive-loss counter and saves the decoded frame
// for future PLC use. Call this after successfully decoding a received packet.
func (j *JitterBuffer) onGoodPacket(frame []int16) {
	j.consecutiveLoss = 0
	j.lastGoodFrame = frame
}

// Reset clears the buffer state (call on session restart or SSRC change).
func (j *JitterBuffer) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = j.buf[:0]
	j.primed = false
	j.nextSeq = 0
	j.consecutiveLoss = 0
	j.lastGoodFrame = nil
}

// seqLess compares RTP sequence numbers accounting for 16-bit wraparound.
func seqLess(a, b uint16) bool {
	diff := int32(a) - int32(b)
	return diff < 0 || diff > maxSeqDrift
}
