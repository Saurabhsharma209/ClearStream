package sip

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServeHTTP_Start_InboundSessionCreateError covers the previously-0%
// error branch in handleStart where rtppkg.NewSession fails to create the
// inbound (caller -> ClearStream) session -- e.g. because InboundAddr can't
// be resolved as a UDP address. handleStart must respond 500 with a JSON
// error body and must not register a session.
func TestServeHTTP_Start_InboundSessionCreateError(t *testing.T) {
	p := newTestProxy(t)
	agentStream := freeUDPPort(t)
	// "not a valid addr!!" has no port and isn't a valid host:port pair,
	// so net.ResolveUDPAddr fails inside rtppkg.NewSession's ListenAddr
	// resolution, exercising the handleStart 500 path.
	bodyStr := "{\"call_id\":\"bad-addr-call\",\"inbound_addr\":\"not a valid addr!!\",\"agentstream_addr\":\"" + agentStream + "\"}"
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(bodyStr))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	var errBody map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errBody); err != nil {
		t.Fatalf("expected valid JSON error body, decode failed: %v", err)
	}
	if errBody["error"] == "" {
		t.Errorf("expected non-empty error message in response body")
	}

	if p.ActiveSessions() != 0 {
		t.Errorf("expected 0 active sessions after failed start, got %d", p.ActiveSessions())
	}
}
