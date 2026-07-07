package http_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/telemetry"
	"go.uber.org/zap"
)

// telFakeSink records every metric/event it receives; safe for concurrent use.
type telFakeSink struct {
	mu      sync.Mutex
	metrics []telemetry.Metric
	events  []telemetry.Event
}

func (f *telFakeSink) RecordMetric(m telemetry.Metric) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metrics = append(f.metrics, m)
}

func (f *telFakeSink) RecordEvent(e telemetry.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *telFakeSink) countMetric(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.metrics {
		if m.Name == name {
			n++
		}
	}
	return n
}

// TestHandleEnhance_RecordsErrorMetricOnFailure verifies that a failed
// /enhance request (missing the required "audio" form field) records
// MetricErrorsTotal, and that the request latency timer records
// MetricFrameLatencyMS regardless of success or failure.
func TestHandleEnhance_RecordsErrorMetricOnFailure(t *testing.T) {
	sink := &telFakeSink{}
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
		Telemetry:  sink,
	})

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("other", "value") //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/enhance", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if sink.countMetric(telemetry.MetricErrorsTotal) == 0 {
		t.Error("expected MetricErrorsTotal to be recorded on request failure")
	}
	if sink.countMetric(telemetry.MetricFrameLatencyMS) == 0 {
		t.Error("expected MetricFrameLatencyMS timer to be recorded")
	}
}

// TestHandleEnhance_NoTelemetryDefaultsToNoop verifies the handler does not
// panic when no Telemetry sink is configured.
func TestHandleEnhance_NoTelemetryDefaultsToNoop(t *testing.T) {
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
