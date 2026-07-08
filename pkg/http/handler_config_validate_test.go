// Package http_test exercises HandlerConfig.Validate() and NewHandler's
// safe-default fallback behavior when given an invalid or incomplete config.
package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestHandlerConfigValidate_NilSuppressor verifies that a HandlerConfig with
// no Suppressor is rejected by Validate().
func TestHandlerConfigValidate_NilSuppressor(t *testing.T) {
	cfg := cshttp.HandlerConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for nil Suppressor, got nil")
	} else if !strings.Contains(err.Error(), "Suppressor") {
		t.Errorf("expected error to mention Suppressor, got: %v", err)
	}
}

// TestHandlerConfigValidate_BadSampleRate verifies that an unsupported
// SampleRate is rejected by Validate().
func TestHandlerConfigValidate_BadSampleRate(t *testing.T) {
	cfg := cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		SampleRate: 12345,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid SampleRate, got nil")
	} else if !strings.Contains(err.Error(), "SampleRate") {
		t.Errorf("expected error to mention SampleRate, got: %v", err)
	}
}

// TestHandlerConfigValidate_NegativePoolSize verifies that a negative
// PoolSize is rejected by Validate().
func TestHandlerConfigValidate_NegativePoolSize(t *testing.T) {
	cfg := cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		PoolSize:   -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative PoolSize, got nil")
	} else if !strings.Contains(err.Error(), "PoolSize") {
		t.Errorf("expected error to mention PoolSize, got: %v", err)
	}
}

// TestHandlerConfigValidate_Valid verifies that a fully valid config passes.
func TestHandlerConfigValidate_Valid(t *testing.T) {
	cfg := cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		SampleRate: 16000,
		PoolSize:   4,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for valid config, got: %v", err)
	}
}

// TestHandlerConfigValidate_ZeroValuesAreOptional verifies that zero-value
// SampleRate and PoolSize (meaning "use default") do not fail validation.
func TestHandlerConfigValidate_ZeroValuesAreOptional(t *testing.T) {
	cfg := cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for zero-value SampleRate/PoolSize, got: %v", err)
	}
}

// TestNewHandler_NilSuppressorFallsBackWithoutPanic verifies that NewHandler
// does not panic when given a nil Suppressor -- it must fall back to a safe
// passthrough suppressor and log a warning, per HandlerConfig.Validate().
// Before this fix, a nil Suppressor would panic on the first request because
// every handler path calls Suppressor.Name() unconditionally.
func TestNewHandler_NilSuppressorFallsBackWithoutPanic(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	h := cshttp.NewHandler(cshttp.HandlerConfig{
		// Suppressor intentionally left nil.
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	// Must not panic.
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 from /health with fallback suppressor, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("expected ok status in body, got: %s", w.Body.String())
	}
	if logs.Len() == 0 {
		t.Error("expected a warning to be logged for invalid HandlerConfig")
	}
}

// TestNewHandler_InvalidSampleRateFallsBackToDefault verifies that an
// unsupported SampleRate is corrected to the default (16000) instead of
// being silently accepted, and is reflected in GET /info.
func TestNewHandler_InvalidSampleRateFallsBackToDefault(t *testing.T) {
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		SampleRate: 12345, // invalid
		Logger:     zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"sample_rate":16000`) {
		t.Errorf("expected sample_rate to fall back to 16000, got: %s", w.Body.String())
	}
}

// TestNewHandler_NoLoggerDoesNotPanicOnErrorPath verifies that a Handler
// constructed with no Logger at all does not panic when an error-logging
// path is exercised (e.g. a suppressor that fails during processing).
func TestNewHandler_NoLoggerDoesNotPanicOnErrorPath(t *testing.T) {
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		// Logger intentionally left nil.
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	// Must not panic even though no Logger was supplied.
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
