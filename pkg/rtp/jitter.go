// Package rtp provides real-time RTP stream interception and noise suppression.
package rtp

import (
	"math"
	"sync"
	"time"
)

const (
	defaultJitterDepth    = 4   // initial target buffer depth (frames)
	defaultJitterMaxDepth = 32  // hard cap before tail-drop
	maxSeqDrift           = 500 // seq-number gap that signals reset/wrap
	minAdaptDepth         = 2   // adaptive lower bound (20ms)
	maxAdaptDepth         = 16  // adaptive upper bound (160ms)
)

// jitterEntry holds a buffered RTP packet.
type jitterEntry struct {
	seq       uint16
	timestamp uint32
	payload   []byte
	received  time.Time
}

// JitterBuffer smooths out packet arrival variance for real-time audio.
//
// Key improvements over naïve fixed-depth buffer:
//   - O(n) insertion: new packet inserted in-place rather than full sort each Push
//   - Adaptive depth: inter-arrival variance drives automatic grow/shrink of depth
//   - Waveform-substitution PLC: pitch-period repeat for the first 2 loss frames,
//     then exponential fade-to-silence (sounds far more natural than immediate fade)
//   - Mutex guarding on generatePLC/onGoodPacket so callers need no extra locking
type JitterBuffer struct {
	mu       sync.Mutex
	buf      []jitterEntry
	nextSeq  uint16
	depth    int // current target buffer depth (frames), adapts over time
	maxDepth int
	primed   bool

	// PLC state
	consecutiveLoss int
	lastGoodFrame   []int16

	// Adaptive depth tracking
	lastArrival    time.Time
	arrivalVarMs   float64 // exponential moving average of inter-arrival variance
	arrivalEMAMs   float64 // EMA of inter-arrival time (ms)
	adaptFrames    int     // frame counter for depth adjustment hysteresis
}

// NewJitterBuffer creates a jitter buffer with the given initial target depth.
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

// Push inserts an incoming RTP packet into the buffer using O(n) insertion
// (binary-search position then shift), avoiding the O(n log n) sort on every packet.
// Returns true once the buffer has accumulated enough packets to start draining.
func (j *JitterBuffer) Push(seq uint16, ts uint32, payload []byte) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	now := time.Now()

	// Track inter-arrival variance for adaptive depth.
	if !j.lastArrival.IsZero() {
		iaMs := float64(now.Sub(j.lastArrival).Milliseconds())
		// EMA of inter-arrival time
		if j.arrivalEMAMs == 0 {
			j.arrivalEMAMs = iaMs
		} else {
			j.arrivalEMAMs = 0.9*j.arrivalEMAMs + 0.1*iaMs
		}
		// EMA of variance (mean absolute deviation from EMA)
		dev := math.Abs(iaMs - j.arrivalEMAMs)
		j.arrivalVarMs = 0.95*j.arrivalVarMs + 0.05*dev
	}
	j.lastArrival = now

	// Copy payload to avoid aliasing with caller's buffer.
	p := make([]byte, len(payload))
	copy(p, payload)
	entry := jitterEntry{seq: seq, timestamp: ts, payload: p, received: now}

	// O(n) sorted insertion: find position via linear scan (buffer is short,
	// typically 2–16 entries; binary search overhead not worth it).
	pos := len(j.buf)
	for i, e := range j.buf {
		if seqLess(seq, e.seq) {
			pos = i
			break
		}
	}
	j.buf = append(j.buf, jitterEntry{}) // grow by one
	copy(j.buf[pos+1:], j.buf[pos:])     // shift right
	j.buf[pos] = entry

	// Tail-drop if over hard cap.
	if len(j.buf) > j.maxDepth {
		j.buf = j.buf[:j.maxDepth]
	}

	// Prime once we have enough packets.
	if !j.primed && len(j.buf) >= j.depth {
		j.primed = true
		j.nextSeq = j.buf[0].seq
	}

	// Adapt depth every 50 frames (~500ms).
	j.adaptFrames++
	if j.adaptFrames >= 50 {
		j.adaptFrames = 0
		j.adaptDepth()
	}

	return j.primed
}

// adaptDepth adjusts the target buffer depth based on measured inter-arrival
// variance. Depth = ceil(3 × jitter / 10ms), clamped to [minAdaptDepth, maxAdaptDepth].
// Called while mu is held.
func (j *JitterBuffer) adaptDepth() {
	// 3-sigma rule: buffer enough to absorb 3× the observed jitter.
	targetMs := j.arrivalVarMs * 3.0
	targetFrames := int(math.Ceil(targetMs/10.0)) // 10ms per frame
	if targetFrames < minAdaptDepth {
		targetFrames = minAdaptDepth
	}
	if targetFrames > maxAdaptDepth {
		targetFrames = maxAdaptDepth
	}
	j.depth = targetFrames
}

// Pop returns the next expected packet payload.
// Returns (nil, true) on packet loss — caller must invoke GeneratePLC().
// Returns (nil, false) when the buffer is not yet primed or is empty.
func (j *JitterBuffer) Pop() ([]byte, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.primed || len(j.buf) == 0 {
		return nil, false
	}

	head := j.buf[0]
	if head.seq == j.nextSeq {
		j.buf = j.buf[1:]
		j.nextSeq++
		return head.payload, true
	}

	// Gap detected: packet lost or arrived too late (beyond current depth).
	j.nextSeq++
	return nil, true
}

// GeneratePLC produces a packet-loss-concealment frame.
//
// Strategy (mimics WebRTC's PLC behaviour):
//   - Loss 1–2: pitch-period waveform substitution — repeat the tail of the
//     last good frame at the detected pitch period. Sounds like the speaker
//     held the last syllable, which is perceptually natural.
//   - Loss 3+: exponential fade-to-silence (0.85× per frame). Avoids the
//     uncanny loop of repeating audio indefinitely.
//
// Thread-safe; acquires mu internally.
func (j *JitterBuffer) GeneratePLC() []int16 {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.consecutiveLoss++
	if j.lastGoodFrame == nil {
		return make([]int16, 160) // pure silence on first loss with no history
	}

	frameLen := len(j.lastGoodFrame)
	result := make([]int16, frameLen)

	if j.consecutiveLoss <= 2 {
		// Waveform substitution: detect pitch period (40–400 samples for speech)
		// via peak autocorrelation, then copy a pitch cycle forward.
		period := detectPitch(j.lastGoodFrame)
		for i := 0; i < frameLen; i++ {
			src := i % period
			if src < frameLen {
				result[i] = j.lastGoodFrame[src]
			}
		}
	} else {
		// Exponential fade: 0.85× per consecutive lost frame after the 2nd.
		decayFactor := math.Pow(0.85, float64(j.consecutiveLoss-2))
		for i, s := range j.lastGoodFrame {
			result[i] = int16(float64(s) * decayFactor)
		}
	}

	return result
}

// generatePLC is the unexported alias kept for backward compatibility with
// existing session.go call sites. Delegates to GeneratePLC.
func (j *JitterBuffer) generatePLC() []int16 {
	return j.GeneratePLC()
}

// OnGoodPacket resets the consecutive-loss counter and stores the decoded frame
// for PLC use on future losses. Thread-safe; acquires mu internally.
func (j *JitterBuffer) OnGoodPacket(frame []int16) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.consecutiveLoss = 0
	// Keep a copy so the caller can reuse/free their slice.
	cp := make([]int16, len(frame))
	copy(cp, frame)
	j.lastGoodFrame = cp
}

// onGoodPacket is the unexported alias kept for backward compatibility.
func (j *JitterBuffer) onGoodPacket(frame []int16) {
	j.OnGoodPacket(frame)
}

// Depth returns the current adaptive target buffer depth in frames.
func (j *JitterBuffer) Depth() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.depth
}

// JitterMs returns the measured inter-arrival jitter in milliseconds (EMA).
func (j *JitterBuffer) JitterMs() float64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.arrivalVarMs
}

// Reset clears all buffer state. Call on SSRC change or session restart.
func (j *JitterBuffer) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = j.buf[:0]
	j.primed = false
	j.nextSeq = 0
	j.consecutiveLoss = 0
	j.lastGoodFrame = nil
	j.arrivalEMAMs = 0
	j.arrivalVarMs = 0
	j.adaptFrames = 0
	j.depth = defaultJitterDepth
}

// ── helpers ──────────────────────────────────────────────────────────────────

// seqLess compares RTP sequence numbers accounting for 16-bit wraparound.
func seqLess(a, b uint16) bool {
	diff := int32(a) - int32(b)
	return diff < 0 || diff > maxSeqDrift
}

// detectPitch estimates the fundamental period of a speech frame using
// normalised autocorrelation. Returns a period in [40, frameLen/2] samples,
// which corresponds to 250 Hz–200 Hz at 16kHz (typical male/female speech).
// Falls back to frameLen/4 if no clear pitch is found.
func detectPitch(frame []int16) int {
	n := len(frame)
	if n < 80 {
		return n // too short to detect
	}

	minLag := 40  // ~400 Hz at 16kHz
	maxLag := n / 2
	if maxLag > 400 { // cap at 100 Hz (lowest reasonable pitch)
		maxLag = 400
	}

	// Compute energy of full frame for normalisation.
	var energy float64
	for _, s := range frame {
		energy += float64(s) * float64(s)
	}
	if energy < 1.0 {
		return n / 4
	}

	bestLag := n / 4
	bestCorr := -1.0

	for lag := minLag; lag <= maxLag; lag++ {
		var corr float64
		for i := 0; i < n-lag; i++ {
			corr += float64(frame[i]) * float64(frame[i+lag])
		}
		// Normalise by frame energy so loudness doesn't bias the search.
		normCorr := corr / energy
		if normCorr > bestCorr {
			bestCorr = normCorr
			bestLag = lag
		}
	}

	return bestLag
}
