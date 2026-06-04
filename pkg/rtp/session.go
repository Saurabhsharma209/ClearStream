package rtp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// rtpPayloadInfo maps standard RTP payload types to codec and true audio sample rate.
// NOTE: G.722 (PT=9) has RTP clock 8000 per RFC 3551 but real audio is 16kHz.
var rtpPayloadInfo = map[uint8]struct {
	codec      audio.Codec
	sampleRate int
}{
	0:   {audio.CodecG711U, 8000},   // PCMU  — Indian PSTN standard
	3:   {audio.CodecGSM, 8000},     // GSM
	8:   {audio.CodecG711A, 8000},   // PCMA  — Indian PSTN (A-law variant)
	9:   {audio.CodecG722, 16000},   // G.722 — wideband, RTP clock=8000 but audio=16kHz
	15:  {audio.CodecUnknown, 8000}, // G728
	18:  {audio.CodecG729, 8000},    // G.729
	111: {audio.CodecOpus, 48000},   // Opus  — WebRTC default dynamic PT
	110: {audio.CodecOpus, 48000},   // Opus  — alternate dynamic PT
}

// resolvePayloadType fills in Codec and SampleRate from PayloadType if not set.
func resolvePayloadType(cfg *Config) {
	if info, ok := rtpPayloadInfo[cfg.PayloadType]; ok {
		if cfg.Codec == "" {
			cfg.Codec = info.codec
		}
		if cfg.SampleRate == 0 {
			cfg.SampleRate = info.sampleRate
		}
	}
	// Final fallback: unknown PT → narrowband
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 8000
	}
	if cfg.Codec == "" {
		cfg.Codec = audio.CodecG711U
	}
}

// Config holds configuration for a live RTP session.
type Config struct {
	// ListenAddr is the UDP address to receive RTP packets (e.g. ":5004").
	ListenAddr string

	// ForwardAddr is the UDP address to send clean RTP packets to (e.g. "10.0.0.2:5004").
	ForwardAddr string

	// Codec is the expected RTP audio codec. Default: auto-detect from payload type.
	Codec audio.Codec

	// PayloadType is the RTP payload type number (e.g. 0=PCMU, 8=PCMA, 111=Opus).
	// Used when Codec is not specified.
	PayloadType uint8

	// JitterDepth is the number of frames to buffer. Default: 4 (~40ms).
	JitterDepth int

	// FFmpegPath is used for codec transcoding (G.729, G.722, etc.).
	FFmpegPath string

	// SampleRate is set by ClearStream (do not set manually).
	SampleRate int

	// Suppressor is set by ClearStream (do not set manually).
	Suppressor model.Suppressor

	// Logger is set by ClearStream (do not set manually).
	Logger *zap.Logger

	// OnStats is an optional callback called every second with session statistics.
	OnStats func(Stats)

	// DTMFPayloadType is the RTP payload type for telephone events (RFC4733). Default: 101.
	DTMFPayloadType uint8

	// OnDTMF is an optional callback invoked when a DTMF digit is detected.
	OnDTMF func(DTMFDigit)

	// AGC enables Automatic Gain Control on this RTP session.
	// When set, output level is adaptively adjusted toward AGC.TargetRMS.
	// Use audio.DefaultAGCConfig() as a starting point.
	// Set to nil to disable (default).
	AGC *audio.AGCConfig
}

// Stats holds per-second session statistics.
type Stats struct {
	PacketsReceived uint64
	PacketsSent     uint64
	PacketsLost     uint64
	LatencyAvgMs    float64
}

// Session is a live RTP interception session.
// It reads RTP from ListenAddr, suppresses noise, forwards to ForwardAddr.
type Session struct {
	cfg      Config
	conn     *net.UDPConn
	fwdAddr  *net.UDPAddr
	jitter   *JitterBuffer
	pipeline *audio.Pipeline
	dtmf     *DTMFDetector

	currentSSRC uint32
	ssrcSet     bool

	mu        sync.Mutex
	stats     Stats
	RTCPStats RTCPReceiverReport // most recent RTCP RR stats
	rtcpConn  *net.UDPConn
	rtcpReady chan struct{} // closed once rtcpConn is assigned (or binding fails)
	cancel    context.CancelFunc
	done      chan struct{}
	logger    *zap.Logger
}

// NewSession creates (but does not start) an RTP session.
func NewSession(cfg Config) (*Session, error) {
	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("rtp: ListenAddr required")
	}
	if cfg.ForwardAddr == "" {
		return nil, fmt.Errorf("rtp: ForwardAddr required")
	}

	fwdAddr, err := net.ResolveUDPAddr("udp", cfg.ForwardAddr)
	if err != nil {
		return nil, fmt.Errorf("rtp: resolve ForwardAddr: %w", err)
	}

	listenAddr, err := net.ResolveUDPAddr("udp", cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("rtp: resolve ListenAddr: %w", err)
	}

	conn, err := net.ListenUDP("udp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("rtp: listen %s: %w", cfg.ListenAddr, err)
	}

	resolvePayloadType(&cfg)
	if cfg.DTMFPayloadType == 0 {
		cfg.DTMFPayloadType = DTMFPayloadType
	}

	pipe := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:      cfg.SampleRate,
		InputSampleRate: cfg.SampleRate,
		Channels:        1,
		Suppressor:      cfg.Suppressor,
		Logger:          cfg.Logger,
		AGC:             cfg.AGC,
	})

	return &Session{
		cfg:       cfg,
		conn:      conn,
		fwdAddr:   fwdAddr,
		jitter:    NewJitterBuffer(cfg.JitterDepth),
		pipeline:  pipe,
		dtmf:      NewDTMFDetector(cfg.SampleRate),
		done:      make(chan struct{}),
		rtcpReady: make(chan struct{}),
		logger:    cfg.Logger,
	}, nil
}

// Start begins processing RTP packets. Non-blocking; runs in background goroutines.
func (s *Session) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	go s.receiveLoop(ctx)
	go s.listenRTCP()
	if s.cfg.OnStats != nil {
		go s.statsLoop(ctx)
	}

	s.logger.Info("RTP session started",
		zap.String("listen", s.cfg.ListenAddr),
		zap.String("forward", s.cfg.ForwardAddr),
		zap.String("codec", string(s.cfg.Codec)),
	)
}

// Stop gracefully shuts down the session.
func (s *Session) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.dtmf.Reset()
	s.conn.Close()
	// Wait for listenRTCP to finish binding before accessing rtcpConn.
	<-s.rtcpReady
	s.mu.Lock()
	rc := s.rtcpConn
	s.mu.Unlock()
	if rc != nil {
		rc.Close()
	}
	<-s.done
	s.logger.Info("RTP session stopped")
}

// Stats returns a snapshot of current session statistics.
func (s *Session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// receiveLoop is the main packet processing loop.
func (s *Session) receiveLoop(ctx context.Context) {
	defer close(s.done)

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			s.logger.Error("rtp read error", zap.Error(err))
			return
		}

		start := time.Now()
		if err := s.handlePacket(buf[:n]); err != nil {
			s.logger.Warn("packet processing error", zap.Error(err))
		}

		latency := time.Since(start).Seconds() * 1000
		s.mu.Lock()
		s.stats.PacketsReceived++
		// Simple exponential moving average for latency
		s.stats.LatencyAvgMs = s.stats.LatencyAvgMs*0.9 + latency*0.1
		s.mu.Unlock()
	}
}

// handlePacket parses an RTP packet, suppresses noise, and forwards it.
func (s *Session) handlePacket(raw []byte) error {
	if len(raw) < 12 {
		return fmt.Errorf("packet too short: %d bytes", len(raw))
	}

	// Parse RTP header (RFC 3550)
	header, payload, err := parseRTPHeader(raw)
	if err != nil {
		return err
	}

	// Handle DTMF telephone-event packets (RFC4733)
	if header.PayloadType == s.cfg.DTMFPayloadType {
		digit, err := s.dtmf.ParseDTMFPayload(payload)
		if err != nil {
			s.logger.Warn("dtmf parse error", zap.Error(err))
		} else if digit != nil && s.cfg.OnDTMF != nil {
			s.cfg.OnDTMF(*digit)
		}
		return nil
	}

	// Detect SSRC change (new call leg)
	if s.ssrcSet && header.SSRC != s.currentSSRC {
		s.logger.Info(fmt.Sprintf("SSRC changed: %d → %d, pipeline reset", s.currentSSRC, header.SSRC))
		s.jitter.Reset()
		s.pipeline.Reset()
	}
	s.currentSSRC = header.SSRC
	s.ssrcSet = true

	// Push into jitter buffer
	ready := s.jitter.Push(header.SequenceNumber, header.Timestamp, payload)
	if !ready {
		return nil // still buffering
	}

	// Pop next frame
	frame, ok := s.jitter.Pop()
	if !ok {
		return nil
	}

	var pcm []int16

	if frame == nil {
		// Packet loss — fade-to-silence PLC (0.9x decay per consecutive loss)
		pcm = s.jitter.generatePLC()
		s.mu.Lock()
		s.stats.PacketsLost++
		s.mu.Unlock()
	} else {
		// Decode payload to 16kHz mono PCM
		pcm, err = s.decodeToPCM(frame, header.PayloadType)
		if err != nil {
			return fmt.Errorf("decode payload: %w", err)
		}
	}

	// Run suppressor
	var cleanBuf bytes.Buffer
	rawBytes := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		rawBytes[2*i] = byte(v)
		rawBytes[2*i+1] = byte(v >> 8)
	}
	if err := s.pipeline.ProcessFrames(rawBytes, &cleanBuf); err != nil {
		return fmt.Errorf("suppress: %w", err)
	}

	cleanPCM := bytesToInt16Slice(cleanBuf.Bytes())
	if len(cleanPCM) > 0 {
		s.jitter.onGoodPacket(cleanPCM)
	}

	// Re-encode to original codec
	outPayload, err := s.encodeFromPCM(cleanPCM, header.PayloadType)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	// Rebuild and forward RTP packet
	outRaw := buildRTPPacket(header, outPayload)
	if _, err := s.conn.WriteToUDP(outRaw, s.fwdAddr); err != nil {
		return fmt.Errorf("forward: %w", err)
	}

	s.mu.Lock()
	s.stats.PacketsSent++
	s.mu.Unlock()

	return nil
}

// decodeToPCM decodes a codec-specific RTP payload to 16kHz mono int16 PCM.
func (s *Session) decodeToPCM(payload []byte, pt uint8) ([]int16, error) {
	codec := s.cfg.Codec
	if codec == audio.CodecUnknown {
		codec = payloadTypeToCodec(pt)
	}

	switch codec {
	case audio.CodecG711U:
		return decodeG711U(payload), nil
	case audio.CodecG711A:
		return decodeG711A(payload), nil
	case audio.CodecPCM:
		return bytesToInt16Slice(payload), nil
	case audio.CodecOpus, audio.CodecG722, audio.CodecG729:
		// Use FFmpeg for complex codecs: write payload → temp file → decode → PCM
		return decodeViaFFmpeg(s.cfg.FFmpegPath, payload, codec, s.cfg.SampleRate)
	default:
		// Best effort: treat as raw PCM
		return bytesToInt16Slice(payload), nil
	}
}

// encodeFromPCM encodes 16kHz PCM back to the original codec payload.
func (s *Session) encodeFromPCM(pcm []int16, pt uint8) ([]byte, error) {
	codec := s.cfg.Codec
	if codec == audio.CodecUnknown {
		codec = payloadTypeToCodec(pt)
	}

	switch codec {
	case audio.CodecG711U:
		return encodeG711U(pcm), nil
	case audio.CodecG711A:
		return encodeG711A(pcm), nil
	case audio.CodecPCM:
		return int16SliceToBytes(pcm), nil
	case audio.CodecOpus, audio.CodecG722, audio.CodecG729:
		return encodeViaFFmpeg(s.cfg.FFmpegPath, pcm, codec, s.cfg.SampleRate)
	default:
		return int16SliceToBytes(pcm), nil
	}
}

// listenRTCP listens on ListenAddr port+1 for RTCP Receiver Reports.
func (s *Session) listenRTCP() {
	host, portStr, err := net.SplitHostPort(s.cfg.ListenAddr)
	if err != nil {
		close(s.rtcpReady)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		close(s.rtcpReady)
		return
	}
	rtcpAddr := net.JoinHostPort(host, strconv.Itoa(port+1))

	addr, err := net.ResolveUDPAddr("udp", rtcpAddr)
	if err != nil {
		close(s.rtcpReady)
		return
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		close(s.rtcpReady)
		return
	}
	s.mu.Lock()
	s.rtcpConn = conn
	s.mu.Unlock()
	close(s.rtcpReady)
	defer conn.Close()

	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		rr, err := ParseRTCPReceiverReport(buf[:n])
		if err != nil {
			s.logger.Warn("rtcp parse error", zap.Error(err))
			continue
		}
		if rr != nil {
			s.mu.Lock()
			s.RTCPStats = *rr
			s.mu.Unlock()
			s.logger.Info("RTCP receiver report",
				zap.Float64("loss_pct", rr.FractionLost*100),
				zap.Int32("cumulative_lost", rr.CumulativeLost),
				zap.Uint32("jitter_samples", rr.Jitter),
			)
		}
	}
}

// QualityReport returns a human-readable summary of session quality metrics,
// combining RTP stats and pipeline processing stats.
func (s *Session) QualityReport() string {
	rtp := s.Stats()
	pipe := s.pipeline.Stats()
	lossRate := float64(0)
	if rtp.PacketsReceived > 0 {
		lossRate = float64(rtp.PacketsLost) / float64(rtp.PacketsReceived) * 100
	}
	band := "narrowband(8kHz)"
	if s.cfg.SampleRate >= 16000 {
		band = "wideband(16kHz)"
	}
	if s.cfg.SampleRate >= 44100 {
		band = "fullband(48kHz)"
	}
	return fmt.Sprintf(
		"RTP: rx=%d tx=%d lost=%d(%.1f%%) latency=%.1fms | Jitter: %.1fms (depth=%d frames) | Band: %s [PT=%d %s] | Pipeline: %s",
		rtp.PacketsReceived, rtp.PacketsSent, rtp.PacketsLost, lossRate,
		rtp.LatencyAvgMs, s.jitter.JitterMs(), s.jitter.Depth(),
		band, s.cfg.PayloadType, s.cfg.Codec, pipe.String(),
	)
}

func (s *Session) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			snap := s.stats
			s.mu.Unlock()
			s.cfg.OnStats(snap)
		}
	}
}

// ---- RTP header parsing -----------------------------------------------------

type rtpHeader struct {
	Version        uint8
	Padding        bool
	Extension      bool
	CSRCCount      uint8
	Marker         bool
	PayloadType    uint8
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
	CSRCs          []uint32
}

func parseRTPHeader(raw []byte) (rtpHeader, []byte, error) {
	if len(raw) < 12 {
		return rtpHeader{}, nil, fmt.Errorf("rtp: packet too short")
	}

	h := rtpHeader{}
	h.Version = (raw[0] >> 6) & 0x3
	h.Padding = (raw[0]>>5)&0x1 == 1
	h.Extension = (raw[0]>>4)&0x1 == 1
	h.CSRCCount = raw[0] & 0xF
	h.Marker = (raw[1]>>7)&0x1 == 1
	h.PayloadType = raw[1] & 0x7F
	h.SequenceNumber = binary.BigEndian.Uint16(raw[2:4])
	h.Timestamp = binary.BigEndian.Uint32(raw[4:8])
	h.SSRC = binary.BigEndian.Uint32(raw[8:12])

	offset := 12 + int(h.CSRCCount)*4
	if h.Extension && len(raw) > offset+4 {
		extLen := int(binary.BigEndian.Uint16(raw[offset+2:offset+4])) * 4
		offset += 4 + extLen
	}

	if offset > len(raw) {
		return h, nil, fmt.Errorf("rtp: header exceeds packet length")
	}

	return h, raw[offset:], nil
}

func buildRTPPacket(h rtpHeader, payload []byte) []byte {
	buf := make([]byte, 12+len(payload))
	buf[0] = (h.Version << 6) | (boolByte(h.Padding) << 5) | (boolByte(h.Extension) << 4) | h.CSRCCount
	buf[1] = (boolByte(h.Marker) << 7) | h.PayloadType
	binary.BigEndian.PutUint16(buf[2:4], h.SequenceNumber)
	binary.BigEndian.PutUint32(buf[4:8], h.Timestamp)
	binary.BigEndian.PutUint32(buf[8:12], h.SSRC)
	copy(buf[12:], payload)
	return buf
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// ---- Codec helpers ----------------------------------------------------------

// payloadTypeToCodec maps IANA RTP payload type numbers to Codec constants.
func payloadTypeToCodec(pt uint8) audio.Codec {
	switch pt {
	case 0:
		return audio.CodecG711U // PCMU
	case 8:
		return audio.CodecG711A // PCMA
	case 9:
		return audio.CodecG722
	case 18:
		return audio.CodecG729
	case 111, 120: // common dynamic type for Opus
		return audio.CodecOpus
	default:
		return audio.CodecG711U // safest fallback for telephony
	}
}

// G.711 µ-law (PCMU) decoder — RFC 3551
func decodeG711U(payload []byte) []int16 {
	out := make([]int16, len(payload))
	for i, b := range payload {
		out[i] = ulawToLinear(b)
	}
	return out
}

func ulawToLinear(ulaw byte) int16 {
	ulaw = ^ulaw
	t := int32((ulaw&0x0F)<<3) + 132
	t <<= (ulaw & 0x70) >> 4
	if ulaw&0x80 != 0 {
		return int16(132 - t)
	}
	return int16(t - 132)
}

// G.711 A-law (PCMA) decoder — RFC 3551
func decodeG711A(payload []byte) []int16 {
	out := make([]int16, len(payload))
	for i, b := range payload {
		out[i] = alawToLinear(b)
	}
	return out
}

func alawToLinear(alaw byte) int16 {
	alaw ^= 0x55
	sign := int16(1)
	if alaw&0x80 != 0 {
		sign = -1
		alaw &^= 0x80
	}
	exponent := (alaw >> 4) & 0x07
	mantissa := alaw & 0x0F
	var t int16
	if exponent == 0 {
		t = int16(mantissa)<<1 | 1
	} else {
		t = (int16(mantissa)|0x10)<<uint(exponent) | (1 << uint(exponent-1))
	}
	return sign * t * 8
}

// G.711 µ-law encoder
func encodeG711U(pcm []int16) []byte {
	out := make([]byte, len(pcm))
	for i, s := range pcm {
		out[i] = linearToUlaw(s)
	}
	return out
}

func linearToUlaw(sample int16) byte {
	const bias = 0x84
	sign := byte(0)
	if sample < 0 {
		sample = -sample
		sign = 0x80
	}
	if sample > 32635 {
		sample = 32635
	}
	s := int(sample) + bias
	var exp byte
	switch {
	case s&0x4000 != 0:
		exp = 7
	case s&0x2000 != 0:
		exp = 6
	case s&0x1000 != 0:
		exp = 5
	case s&0x0800 != 0:
		exp = 4
	case s&0x0400 != 0:
		exp = 3
	case s&0x0200 != 0:
		exp = 2
	case s&0x0100 != 0:
		exp = 1
	default:
		exp = 0
	}
	mantissa := byte((s >> uint(exp+3)) & 0x0F)
	return ^(sign | (exp << 4) | mantissa)
}

// G.711 A-law encoder
func encodeG711A(pcm []int16) []byte {
	out := make([]byte, len(pcm))
	for i, s := range pcm {
		out[i] = linearToAlaw(s)
	}
	return out
}

func linearToAlaw(sample int16) byte {
	sign := byte(0x00) // positive: bit 7 clear (decoder reads bit7=0 as positive)
	if sample < 0 {
		sample = -sample
		sign = 0x80 // negative: bit 7 set (decoder reads bit7=1 as negative)
	}
	t := int(sample) / 8
	if t > 0x0FFF {
		t = 0x0FFF
	}
	var exp, mantissa byte
	if t >= 32 {
		// find exponent: smallest exp >= 1 such that (0x10 << exp) > t
		// equivalently: exp = bit_length(t) - 5
		bl := 0
		for v := t; v > 0; v >>= 1 {
			bl++
		}
		exp = byte(bl - 5)
		if exp < 1 {
			exp = 1
		}
		if exp > 7 {
			exp = 7
		}
		mantissa = byte((t >> uint(exp)) & 0x0F)
	} else {
		exp = 0
		if t > 0 {
			mantissa = byte((t - 1) >> 1)
		}
	}
	return (sign | (exp << 4) | mantissa) ^ 0x55
}

// decodeViaFFmpeg decodes complex codec payloads (Opus, G.722, G.729) to PCM
// by writing the payload to a temp pipe and letting FFmpeg decode it.
func decodeViaFFmpeg(ffmpegPath string, payload []byte, codec audio.Codec, sampleRate int) ([]int16, error) {
	// Map codec to ffmpeg input format
	var inputFmt string
	switch codec {
	case audio.CodecOpus:
		inputFmt = "opus"
	case audio.CodecG722:
		inputFmt = "g722"
	case audio.CodecG729:
		inputFmt = "g729"
	default:
		inputFmt = "s16le"
	}

	cmd := exec.Command(ffmpegPath,
		"-f", inputFmt,
		"-i", "pipe:0",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", "1",
		"-f", "s16le",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode %s: %w", codec, err)
	}
	return bytesToInt16Slice(out), nil
}

// encodeViaFFmpeg encodes PCM to a complex codec (Opus, G.722, G.729).
func encodeViaFFmpeg(ffmpegPath string, pcm []int16, codec audio.Codec, sampleRate int) ([]byte, error) {
	var outputFmt string
	switch codec {
	case audio.CodecOpus:
		outputFmt = "opus"
	case audio.CodecG722:
		outputFmt = "g722"
	case audio.CodecG729:
		outputFmt = "g729"
	default:
		outputFmt = "s16le"
	}

	cmd := exec.Command(ffmpegPath,
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", "1",
		"-i", "pipe:0",
		"-f", outputFmt,
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(int16SliceToBytes(pcm))
	return cmd.Output()
}

// ---- byte/int16 helpers -----------------------------------------------------

func bytesToInt16Slice(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(b[2*i]) | int16(b[2*i+1])<<8
	}
	return out
}

func int16SliceToBytes(s []int16) []byte {
	out := make([]byte, len(s)*2)
	for i, v := range s {
		out[2*i] = byte(v)
		out[2*i+1] = byte(v >> 8)
	}
	return out
}
