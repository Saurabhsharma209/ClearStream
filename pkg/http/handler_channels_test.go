// Package http_test covers HandlerConfig.Channels wiring: previously
// pkg/http hardcoded mono (1 channel) processing in both handleEnhance and
// handleEnhanceDir regardless of clearstream.Config.Channels, so setting
// Channels: 2 on the top-level SDK config had no observable effect on the
// HTTP API even though it worked for ProcessFile/ProcessFileWithOptions and
// Pipeline(). These tests cover the new HandlerConfig.Channels field: its
// validation, its safe-default fallback, and that it is surfaced via
// GET /info.
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

// TestHandlerConfigValidate_BadChannels verifies that an out-of-range
// Channels value is rejected by Validate().
func TestHandlerConfigValidate_BadChannels(t *testing.T) {
	cfg := cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Channels:   3,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid Channels, got nil")
	} else if !strings.Contains(err.Error(), "Channels") {
		t.Errorf("expected error to mention Channels, got: %v", err)
	}
}

// TestHandlerConfigValidate_ChannelsZeroIsOptional verifies that a zero-value
// Channels (meaning "use default") does not fail validation.
func TestHandlerConfigValidate_ChannelsZeroIsOptional(t *testing.T) {
	cfg := cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for zero-value Channels, got: %v", err)
	}
}

// TestHandlerConfigValidate_ChannelsValid verifies that Channels: 1 and
// Channels: 2 both pass validation.
func TestHandlerConfigValidate_ChannelsValid(t *testing.T) {
	for _, ch := range []int{1, 2} {
		cfg := cshttp.HandlerConfig{
			Suppressor: model.NewPassthrough(),
			Channels:   ch,
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected no error for Channels=%d, got: %v", ch, err)
		}
	}
}

// TestNewHandler_InvalidChannelsFallsBackToDefault verifies that an
// out-of-range Channels value is corrected to the default (1) instead of
// being silently accepted, and that the corrected value is reflected in
// GET /info.
func TestNewHandler_InvalidChannelsFallsBackToDefault(t *testing.T) {
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Channels:   5, // invalid
		Logger:     zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"channels":1`) {
		t.Errorf("expected channels to fall back to 1, got: %s", w.Body.String())
	}
}

// TestNewHandler_ChannelsDefaultsToMono verifies that omitting Channels
// entirely defaults to 1 (mono), matching clearstream.Config's default.
func TestNewHandler_ChannelsDefaultsToMono(t *testing.T) {
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		// Channels intentionally left unset.
	})

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"channels":1`) {
		t.Errorf("expected default channels 1, got: %s", w.Body.String())
	}
}

// TestNewHandler_ChannelsReflectsConfiguredStereo verifies that a valid
// Channels: 2 (stereo) configuration is honored end-to-end -- this is the
// regression test for the bug where HandlerConfig had no Channels field at
// all and both handleEnhance and handleEnhanceDir hardcoded Channels: 1 in
// their file.ProcessorConfig, silently discarding the caller's setting.
func TestNewHandler_ChannelsReflectsConfiguredStereo(t *testing.T) {
	h := cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Channels:   2,
	})

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"channels":2`) {
		t.Errorf("expected channels to reflect configured stereo (2), got: %s", w.Body.String())
	}
}
