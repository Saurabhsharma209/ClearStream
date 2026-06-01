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
	"time"

	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
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
	logger      *zap.Logger
	metrics     *Metrics
	promHandler http.Handler

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
	Suppressor model.Suppressor
	FFmpegPath string
	SampleRate int
	Logger     *zap.Logger
}

// NewHandler creates a new HTTP API handler.
func NewHandler(cfg HandlerConfig) *Handler {
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}

	reg := prometheus.NewRegistry()
	h := &Handler{
		suppressor: cfg.Suppressor,
		ffmpegPath: cfg.FFmpegPath,
		sampleRate: cfg.SampleRate,
		logger:     cfg.Logger,
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-ClearStream-Version", "0.1.0")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/enhance":
		h.handleEnhance(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		h.handleHealth(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/metrics":
		h.handleMetrics(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/metrics/prometheus":
		h.promHandler.ServeHTTP(w, r)
		return
	default:
		writeError(w, http.StatusNotFound, "endpoint not found")
	}
}

// handleEnhance processes POST /enhance.
// Accepts: multipart/form-data with field "audio" (any format).
// Returns: enhanced audio file (same format as input).
// AgentStream calls this to clean recorded call segments before STT.
func (h *Handler) handleEnhance(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h.metrics.RequestsTotal++
	h.reqTotal.Inc()

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		writeError(w, http.StatusBadRequest, "failed to parse form: "+err.Error())
		return
	}

	f, header, err := r.FormFile("audio")
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
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
		writeError(w, http.StatusInternalServerError, "temp file error")
		return
	}
	defer os.Remove(tmpIn.Name())
	defer tmpIn.Close()

	if _, err := io.Copy(tmpIn, f); err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		writeError(w, http.StatusInternalServerError, "upload read error")
		return
	}
	tmpIn.Close()

	// Create output temp file.
	tmpOut, err := os.CreateTemp("", "cs-out-*"+ext)
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
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

	if err := proc.ProcessWithOptions(tmpIn.Name(), tmpOut.Name(), opts); err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		h.logger.Error("enhance failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "enhancement failed: "+err.Error())
		return
	}

	// Stream result back to caller.
	outFile, err := os.Open(tmpOut.Name())
	if err != nil {
		h.metrics.RequestsFailed++
		h.reqFailed.Inc()
		writeError(w, http.StatusInternalServerError, "output read error")
		return
	}
	defer outFile.Close()

	contentType := extToMIME(ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="enhanced%s"`, ext))
	w.Header().Set("X-Processing-Ms", fmt.Sprintf("%.0f", time.Since(start).Seconds()*1000))
	w.WriteHeader(http.StatusOK)
	io.Copy(w, outFile) //nolint:errcheck

	elapsed := time.Since(start).Seconds() * 1000
	h.metrics.RequestsOK++
	h.metrics.AvgProcessingMs = h.metrics.AvgProcessingMs*0.9 + elapsed*0.1
	h.reqOK.Inc()
	h.procDuration.Observe(time.Since(start).Seconds())

	h.logger.Info("enhanced audio",
		zap.String("file", header.Filename),
		zap.Float64("ms", elapsed),
	)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"status":          "ok",
		"version":         "0.1.0",
		"model":           h.suppressor.Name(),
		"uptime":          time.Since(h.metrics.startTime).Round(time.Second).String(),
		"active_sessions": h.metrics.ActiveSessions,
	})
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	h.metrics.Uptime = time.Since(h.metrics.startTime).Round(time.Second).String()
	json.NewEncoder(w).Encode(h.metrics) //nolint:errcheck
}

// ---- helpers ----------------------------------------------------------------

func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

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
