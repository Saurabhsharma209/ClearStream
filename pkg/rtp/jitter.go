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
	mu           sync.Mutex
	buf          []jitterEntry
	nextSeq      uint16
	initialDepth int // depth passed to NewJitterBuffer; restored on Reset()
	depth        int // current target buffer depth (frames), adapts over time
	maxDepth     int
	primed       bool

	// PLC state
	consecutiveLoss int
	lastGoodFrame   []int16
	prevPLC         []int16 // last generated PLC frame; used as fade source to guarantee monotonic attenuation

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
		initialDepth: depth,
		depth:        depth,
		maxDepth:     defaultJitterMaxDepth,
		buf:          make([]jitterEntry, 0, depth*2),
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

	// Adapt depth every 100 frames (~1s).
	// AQ-002: raised from 50→100 frames so bursty Wi-Fi jitter doesn't cause
	// rapid depth oscillation (perceived as choppy playout rhythm).
	j.adaptFrames++
	if j.adaptFrames >= 100 {
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
		// via peak autocorrelation, then copy the tail pitch cycle forward.
		// We copy from the TAIL of lastGoodFrame (most recent samples) so the
		// substituted audio continues naturally from where the last frame ended.
		period := detectPitch(j.lastGoodFrame)
		tail := frameLen - period
		if tail < 0 {
			tail = 0
		}
		for i := 0; i < frameLen; i++ {
			result[i] = j.lastGoodFrame[tail+(i%period)]
		}
	} else {
		// Exponential fade: 0.85× per frame, applied to the PREVIOUS PLC frame
		// (not lastGoodFrame). This guarantees strict monotonic attenuation
		// regardless of the amplitude of the waveform-substitution frames.
		// Without this, if waveform-sub copied from a low-energy region of the
		// frame, the first fade frame (0.85 × full-amplitude lastGoodFrame) could
		// be louder than the last waveform-sub frame — a non-monotonic jump.
		src := j.prevPLC
		if src == nil {
			src = j.lastGoodFrame
		}
		for i, s := range src {
			result[i] = int16(float64(s) * 0.85)
		}
	}

	j.prevPLC = result
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
	j.prevPLC = nil // clear PLC state so next loss starts fresh from lastGoodFrame
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
	j.prevPLC = nil
	j.lastArrival = time.Time{} // CS-002: zero so first post-reset packet doesn't compute stale inter-arrival delta
	j.arrivalEMAMs = 0
	j.arrivalVarMs = 0
	j.adaptFrames = 0
	j.depth = j.initialDepth // restore configured depth, not the package-level default
}

// ── helpers ──────────────────────────────────────────────────────────────────

// seqLess reports whether RTP sequence number a comes before b in stream order,
// correctly handling 16-bit wraparound per RFC 3550 §A.1.
//
// The forward distance from a to b is (b-a) mod 2^16 (uint16 subtraction wraps
// automatically in Go). If that distance is in [1, 32767], a precedes b; if it
// is in [32768, 65535], b precedes a (i.e., b has already wrapped around past a).
//
// This replaces the previous int32-subtraction heuristic which treated post-wrap
// seqnums (e.g. 0 after 65535) as numerically less than pre-wrap seqnums,
// causing them to be inserted at the front of the buffer instead of the back.
func seqLess(a, b uint16) bool {
	dist := b - a // uint16: wraps automatically, gives forward distance a→b
	return dist > 0 && dist < 0x8000
}

// detectPitch estimates the fundamental period of a speech frame using
// normalised autocorrelation. Returns a period in [30, frameLen/2] samples.
//
// AQ-004: Search range widened from [40,400] → [30,450] samples.
//   - Lower bound 30 (was 40): covers ~533 Hz at 16kHz (high female voices,
//     children). The old 40-sample floor missed these, causing the PLC to
//     pick a doubled period (octave error), which sounds like blabbering.
//   - Upper bound 450 (was 400): covers ~35 Hz at 16kHz for very low-pitched
//     voices and tonal background noise that should be repeated faithfully.
//
// Pitch continuity guard (AQ-004): if the detected period deviates more than
// 50% from the previous call's period, the previous period is reused. Octave
// errors (sudden 2× or 0.5× jumps) are the most common PLC artifact causing
// the "blabbering" sound — this guard eliminates them.
//
// Falls back to frameLen/4 if no clear pitch is found.
var prevDetectedPitch int // package-level continuity state (reset is harmless)

func detectPitch(frame []int16) int {
	n := len(frame)
	if n < 80 {
		return n // too short to detect
	}

	minLag := 30  // AQ-004: was 40 — now covers ~533 Hz at 16kHz
	maxLag := n / 2
	if maxLag > 450 { // AQ-004: was 400 — covers ~35 Hz lower bound
		maxLag = 450
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

	// AQ-004: pitch continuity guard — reject octave jumps.
	// If the new period deviates > 50% from the previous period, reuse the
	// previous one. This eliminates the "blabbering" artifact caused by the
	// autocorrelation picking a doubled or halved period on ambiguous frames.
	if prevDetectedPitch > 0 {
		ratio := float64(bestLag) / float64(prevDetectedPitch)
		if ratio > 1.5 || ratio < 0.67 {
			bestLag = prevDetectedPitch // reuse previous — octave jump rejected
		}
	}
	prevDetectedPitch = bestLag
	return bestLag
}
