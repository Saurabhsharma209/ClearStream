package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

func newTestHandler() *cshttp.Handler {
	return cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
}

func TestHealthEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ok") {
		t.Errorf("expected ok in body, got: %s", body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestNotFound(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestEnhanceMissingField(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/enhance",
		strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Should fail with 400 (missing audio field).
	if w.Code == http.StatusOK {
		t.Error("expected non-200 for missing audio field")
	}
}
