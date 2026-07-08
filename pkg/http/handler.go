// Package http provides an HTTP API for ClearStream audio enhancement.
// AgentStream and other services integrate via this API.
package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const (
	maxUploadSize  = 100 << 20 // 100MB
	defaultTimeout = 5 * time.Minute
)

// Handler is the ClearStream HTTP API handler.
// Mount it on your HTTP mux with http.Handle("/", handler).
type Handler struct {
	suppressor  model.Suppressor
	ffmpegPath  string
	sampleRate  int
	poolSize    int
	logger      *zap.Logger
	metrics     *Metrics
	promHandler http.Handler
	telemetry   telemetry.Sink

	// Prometheus metrics
	reqTotal     prometheus.Counter
	reqOK        prometheus.Counter
	reqFailed    prometheus.Counter
	procDuration prometheus.Histogram
}

// Metrics holds real-time API metrics.
type Metrics struct {
	RequestsTotal   int64   `json:"requests_total"`
	RequestsOK      int64   `json:"requests_ok"`
	RequestsFailed  int64   `json:"requests_failed"`
	AvgProcessingMs float64 `json:"avg_processing_ms"`
	ActiveSessions  int     `json:"active_sessions"`
	Uptime          string  `json:"uptime"`
	startTime       time.Time
}

// HandlerConfig configures the HTTP handler.
type HandlerConfig struct {
	// Suppressor performs the actual noise-suppression work. Required --
	// NewHandler falls back to a no-op passthrough suppressor (and logs a
	// warning) if this is left nil, since every request handler calls
	// Suppressor.Name() unconditionally.
	Suppressor model.Suppressor
	// FFmpegPath overrides the ffmpeg binary location. Default: "ffmpeg" (PATH).
	FFmpegPath string
	// SampleRate is the internal processing sample rate for POST /enhance.
	// Must be one of 8000, 16000, 32000, 48000 Hz if set. Default: 16000.
	SampleRate int
	// Logger is an optional zap logger. If nil, a no-op logger is used so
	// the handler never panics on a nil logger call.
	Logger *zap.Logger
	// PoolSize reports the max concurrent RTP sessions (pool capacity) via
	// GET /info. Purely informational -- does not allocate a pool itself.
	PoolSize int

	// Telemetry receives HTTP request latency metrics and error events.
	// Optional -- defaults to a no-op sink when unset.
	Telemetry telemetry.Sink
}

// Validate checks HandlerConfig fields and returns an error describing the
// first invalid value found, mirroring Config.Validate(). NewHandler calls
// this internally and repairs invalid/missing fields with safe defaults
// (logging a warning) rather than panicking, so construction never fails --
// call Validate explicitly beforehand if you want validation errors surfaced
// to your own caller instead of silently corrected.
//
// Rules enforced:
//   - Suppressor must be non-nil (a nil Suppressor would panic on the first
//     request, since every handler path calls Suppressor.Name()).
//   - SampleRate, if non-zero, must be exactly 8000, 16000, 32000, or 48000 Hz.
//   - PoolSize, if non-zero, must be positive (it is caller-reported metadata).
func (c HandlerConfig) Validate() error {
	if c.Suppressor == nil {
		return fmt.Errorf("clearstream/http: HandlerConfig.Suppressor is required")
	}
	validSampleRates := map[int]bool{8000: true, 16000: true, 32000: true, 48000: true}
	if c.SampleRate != 0 && !validSampleRates[c.SampleRate] {
		return fmt.Errorf("clearstream/http: SampleRate %d is not supported; use one of 8000, 16000, 32000, 48000", c.SampleRate)
	}
	if c.PoolSize < 0 {
		return fmt.Errorf("clearstream/http: PoolSize %d must not be negative", c.PoolSize)
	}
	return nil
}

// NewHandler creates a new HTTP API handler.
//
// cfg is validated via HandlerConfig.Validate(); any invalid or missing
// fields (nil Suppressor, out-of-range SampleRate, negative PoolSize) are
// repaired with safe defaults and logged as a warning instead of causing a
// panic on the first request.
func NewHandler(cfg HandlerConfig) *Handler {
	if err := cfg.Validate(); err != nil {
		if cfg.Logger != nil {
			cfg.Logger.Warn("invalid HandlerConfig; applying safe defaults", zap.Error(err))
		}
		if cfg.Suppressor == nil {
			cfg.Suppressor = model.NewPassthrough()
		}
		if cfg.SampleRate != 0 {
			validSampleRates := map[int]bool{8000: true, 16000: true, 32000: true, 48000: true}
			if !validSampleRates[cfg.SampleRate] {
				cfg.SampleRate = 0
			}
		}
		if cfg.PoolSize < 0 {
			cfg.PoolSize = 0
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}

	reg := prometheus.NewRegistry()
	sink := cfg.Telemetry
	if sink == nil {
		sink = telemetry.NoopSink{}
	}
	h := &Handler{
		suppressor: cfg.Suppressor,
		ffmpegPath: cfg.FFmpegPath,
		sampleRate: cfg.SampleRate,
		poolSize:   cfg.PoolSize,
		logger:     cfg.Logger,
		telemetry:  sink,
		metrics: &Metrics{
			startTime: time.Now(),
		},
	}
	h.reqTotal = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "clearstream_requests_total",
		Help: "Total HTTP enhancement requests",
	})
	h.reqOK = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "clearstream_requests_ok_total",
		Help: "Successful enhancements",
	})
	h.reqFailed = promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "clearstream_requests_failed_total",
		Help: "Failed enhancements",
	})
	h.procDuration = promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
		Name:    "clearstream_processing_duration_seconds",
		Help:    "Audio enhancement processing time",
		Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
	})
	h.promHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return h
}

// ServeHTTP routes requests to the appropriate handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers on every response.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle OPTIONS preflight immediately.
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-ClearStream-Version", "0.1.0")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/enhance":
		h.handleEnhance(w, r)
	case r.URL.Path == "/enhance/stream":
		h.handleStream(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		h.handleHealth(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/info":
		h.handleInfo(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/metrics":
		h.handleMetrics(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/metrics/prometheus":
		h.promHandler.ServeHTTP(w, r)
		return
	default:
		writeError(w, http.StatusNotFound, "endpoint not found")
	}
}

// recordError records a telemetry error-counter increment tagged by the
// originating HTTP endpoint.
func (h *Handler) recordError(endpoint string) {
	h.telemetry.RecordMetric(telemetry.Metric{
		Name:      telemetry.MetricErrorsTotal,
		Value:     1,
		Unit:      "count",
		Kind:      telemetry.MetricCounter,
		Tags:      map[string]string{"component": "http", "endpoint": endpoint},
		Timestamp: time.Now(),
	})
}

// handleEnhance processes POST /enhance.
// Accepts: multipart/form-data with field "audio" (any format).
// Returns: enhanced audio file (same format as input).
// AgentStream calls this to clean recorded call segments before STT.
func (h *Handler) handleEnhance(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h.metrics.RequestsTotal++
	h.reqTotal.Inc()
	stopTimer := telemetry.StartTimer(h.telemetry, telemetry.MetricFrameLatencyMS, map[string]string{"component": "http", "endpoint": "/enhance"})
	defer stopTimer()

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		writeError(w, http.StatusBadRequest, "failed to parse form: "+err.Error())
		return
	}

	f, header, err := r.FormFile("audio")
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		writeError(w, http.StatusBadRequest, "missing audio field")
		return
	}
	defer f.Close()

	// Detect output format from filename extension.
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".wav"
	}

	// Write upload to temp file.
	tmpIn, err := os.CreateTemp("", "cs-in-*"+ext)
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		writeError(w, http.StatusInternalServerError, "temp file error")
		return
	}
	defer os.Remove(tmpIn.Name())
	defer tmpIn.Close()

	if _, err := io.Copy(tmpIn, f); err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		writeError(w, http.StatusInternalServerError, "upload read error")
		return
	}
	tmpIn.Close()

	// Create output temp file.
	tmpOut, err := os.CreateTemp("", "cs-out-*"+ext)
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		writeError(w, http.StatusInternalServerError, "temp file error")
		return
	}
	tmpOut.Close()
	defer os.Remove(tmpOut.Name())

	// Process the audio.
	proc := file.NewProcessor(file.ProcessorConfig{
		FFmpegPath: h.ffmpegPath,
		SampleRate: h.sampleRate,
		Channels:   1,
		Suppressor: h.suppressor,
		Logger:     h.logger,
	})

	opts := file.Options{}
	if r.FormValue("audio_only") == "true" {
		opts.AudioOnly = true
	}
	if r.FormValue("normalize_peak") == "true" {
		opts.NormalizePeak = true
	}

	// AGC: enabled via ?agc=true, tuned via ?agc_target_rms=3000&agc_max_gain=4.0
	// &agc_attack_ms=20&agc_release_ms=200
	if r.FormValue("agc") == "true" {
		agcCfg := audio.DefaultAGCConfig()
		if v := r.FormValue("agc_target_rms"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				agcCfg.TargetRMS = f
			}
		}
		if v := r.FormValue("agc_max_gain"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				agcCfg.MaxGain = f
			}
		}
		if v := r.FormValue("agc_attack_ms"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				agcCfg.AttackMs = f
			}
		}
		if v := r.FormValue("agc_release_ms"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				agcCfg.ReleaseMs = f
			}
		}
		opts.AGC = &agcCfg
	}

	if err := proc.ProcessWithOptions(tmpIn.Name(), tmpOut.Name(), opts); err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		h.logger.Error("enhance failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "enhancement failed: "+err.Error())
		return
	}

	// Stream result back to caller.
	outFile, err := os.Open(tmpOut.Name())
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.recordError("/enhance")
		writeError(w, http.StatusInternalServerError, "output read error")
		return
	}
	defer outFile.Close()

	elapsed := time.Since(start).Seconds() * 1000
	contentType := extToMIME(ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="enhanced%s"`, ext))
	w.Header().Set("X-Processing-Ms", fmt.Sprintf("%.0f", elapsed))
	w.Header().Set("X-ClearStream-Model", h.suppressor.Name())
	w.Header().Set("X-ClearStream-Duration-Ms", fmt.Sprintf("%.0f", elapsed))
	w.WriteHeader(http.StatusOK)
	io.Copy(w, outFile) //nolint:errcheck

	h.metrics.RequestsOK++
	h.metrics.AvgProcessingMs = h.metrics.AvgProcessingMs*0.9 + elapsed*0.1
	h.reqOK.Inc()
	h.procDuration.Observe(time.Since(start).Seconds())

	h.logger.Info("enhanced audio",
		zap.String("file", header.Filename),
		zap.Float64("ms", elapsed),
	)
}

// handleHealth processes GET /health, returning a JSON status payload
// including the active suppressor model name and process uptime.
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"status":     "ok",
		"version":    "0.1.0",
		"model":      h.suppressor.Name(),
		"uptime_sec": int64(time.Since(h.metrics.startTime).Seconds()),
	})
}

// handleInfo processes GET /info, returning static SDK metadata (version,
// model, sample rate, supported codecs, and the list of available endpoints).
func (h *Handler) handleInfo(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"version":                 "0.1.0",
		"model":                   h.suppressor.Name(),
		"sample_rate":             h.sampleRate,
		"frame_size_samples":      160,
		"max_concurrent_sessions": h.poolSize,
		"supported_codecs":        []string{"pcmu", "pcma", "g722", "opus"},
		"endpoints": map[string]string{
			"POST /enhance":           "Upload audio file for noise suppression",
			"GET /health":             "Health check",
			"GET /info":               "SDK info",
			"GET /metrics/prometheus": "Prometheus metrics",
		},
	})
}

// handleMetrics processes GET /metrics, returning the JSON Metrics snapshot
// (request counters, average processing time, and uptime).
func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	h.metrics.Uptime = time.Since(h.metrics.startTime).Round(time.Second).String()
	json.NewEncoder(w).Encode(h.metrics) //nolint:errcheck
}

// handleStream processes POST /enhance/stream.
// Accepts raw 16kHz mono PCM in the request body and streams enhanced PCM back
// chunk-by-chunk using Transfer-Encoding: chunked.
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	w.Header().Set("Content-Type", "audio/pcm")
	w.Header().Set("X-ClearStream-Model", h.suppressor.Name())
	flusher, canFlush := w.(http.Flusher)

	opts := file.Options{Suppressor: h.suppressor, Logger: h.logger}
	if err := file.StreamProcess(r.Context(), r.Body, w, opts); err != nil {
		// Can't write error header after streaming has started.
		h.logger.Error("stream enhance failed", zap.Error(err))
		h.recordError("/enhance/stream")
		return
	}
	if canFlush {
		flusher.Flush()
	}
	h.reqOK.Inc()
	elapsed := time.Since(start).Milliseconds()
	_ = elapsed
}

// ---- helpers ----------------------------------------------------------------

// writeJSONError writes a JSON error body of the form {"error":..,"code":..}
// with the given HTTP status code.
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q,"code":%d}`, msg, code) //nolint:errcheck
}

// writeError is an alias for writeJSONError, used by handlers that write
// the error before setting a Content-Type explicitly.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSONError(w, code, msg)
}

// extToMIME maps a file extension (including the leading dot) to its MIME
// type for the Content-Type response header. Falls back to
// "application/octet-stream" for unrecognised extensions.
func extToMIME(ext string) string {
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".aac", ".m4a":
		return "audio/aac"
	case ".flac":
		return "audio/flac"
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	default:
		return "application/octet-stream"
	}
}
