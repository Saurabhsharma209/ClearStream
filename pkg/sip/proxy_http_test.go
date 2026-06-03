package sip

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// freeUDPPort returns an available UDP address on localhost.
func freeUDPPort(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeUDPPort: %v", err)
	}
	addr := conn.LocalAddr().String()
	conn.Close()
	return addr
}

func newTestProxy(t *testing.T) *Proxy {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	return NewProxy(model.NewPassthrough(), logger)
}

// buildStartBody returns a valid SessionRequest JSON body with free UDP ports.
func buildStartBody(t *testing.T, callID string) string {
	t.Helper()
	inbound := freeUDPPort(t)
	agentStream := freeUDPPort(t)
	return fmt.Sprintf(`{"call_id":%q,"inbound_addr":%q,"agentstream_addr":%q}`,
		callID, inbound, agentStream)
}

// ---- ServeHTTP routing ----

func TestServeHTTP_Health(t *testing.T) {
	p := newTestProxy(t)
	req := httptest.NewRequest(http.MethodGet, "/sip/health", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %q", body["status"])
	}
}

func TestServeHTTP_NotFound(t *testing.T) {
	p := newTestProxy(t)
	req := httptest.NewRequest(http.MethodGet, "/sip/unknown", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ---- handleList ----

func TestServeHTTP_List_Empty(t *testing.T) {
	p := newTestProxy(t)
	req := httptest.NewRequest(http.MethodGet, "/sip/sessions", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var list []interface{}
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

func TestServeHTTP_List_AfterStart(t *testing.T) {
	p := newTestProxy(t)

	body := buildStartBody(t, "list-call-1")
	startReq := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	startW := httptest.NewRecorder()
	p.ServeHTTP(startW, startReq)
	if startW.Code != http.StatusOK {
		t.Fatalf("start failed: %d %s", startW.Code, startW.Body.String())
	}
	defer func() {
		stopReq := httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id=list-call-1", nil)
		p.ServeHTTP(httptest.NewRecorder(), stopReq)
	}()

	listReq := httptest.NewRequest(http.MethodGet, "/sip/sessions", nil)
	listW := httptest.NewRecorder()
	p.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list failed: %d", listW.Code)
	}
	type info struct {
		CallID string `json:"call_id"`
		Codec  string `json:"codec"`
	}
	var list []info
	if err := json.NewDecoder(listW.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
	if list[0].CallID != "list-call-1" {
		t.Errorf("unexpected call_id: %s", list[0].CallID)
	}
}

// ---- handleStart ----

func TestServeHTTP_Start_Valid(t *testing.T) {
	p := newTestProxy(t)
	body := buildStartBody(t, "start-call-1")
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp SessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CallID != "start-call-1" {
		t.Errorf("unexpected call_id: %s", resp.CallID)
	}
	if resp.Status != "started" {
		t.Errorf("unexpected status: %s", resp.Status)
	}
	if p.ActiveSessions() != 1 {
		t.Errorf("expected 1 active session, got %d", p.ActiveSessions())
	}
	stopReq := httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id=start-call-1", nil)
	p.ServeHTTP(httptest.NewRecorder(), stopReq)
}

func TestServeHTTP_Start_WithOutboundAddr(t *testing.T) {
	p := newTestProxy(t)
	inbound := freeUDPPort(t)
	agentStream := freeUDPPort(t)
	outbound := freeUDPPort(t)
	caller := freeUDPPort(t)
	body := fmt.Sprintf(
		`{"call_id":"outbound-call","inbound_addr":%q,"agentstream_addr":%q,"outbound_addr":%q,"caller_addr":%q}`,
		inbound, agentStream, outbound, caller)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if p.ActiveSessions() != 1 {
		t.Errorf("expected 1 session, got %d", p.ActiveSessions())
	}
	stopReq := httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id=outbound-call", nil)
	p.ServeHTTP(httptest.NewRecorder(), stopReq)
}

func TestServeHTTP_Start_MalformedJSON(t *testing.T) {
	p := newTestProxy(t)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader("{not json}"))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServeHTTP_Start_MissingCallID(t *testing.T) {
	p := newTestProxy(t)
	inbound := freeUDPPort(t)
	agentStream := freeUDPPort(t)
	body := fmt.Sprintf(`{"inbound_addr":%q,"agentstream_addr":%q}`, inbound, agentStream)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing call_id, got %d", w.Code)
	}
}

func TestServeHTTP_Start_MissingInboundAddr(t *testing.T) {
	p := newTestProxy(t)
	agentStream := freeUDPPort(t)
	body := fmt.Sprintf(`{"call_id":"x","agentstream_addr":%q}`, agentStream)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing inbound_addr, got %d", w.Code)
	}
}

func TestServeHTTP_Start_MissingAgentStreamAddr(t *testing.T) {
	p := newTestProxy(t)
	inbound := freeUDPPort(t)
	body := fmt.Sprintf(`{"call_id":"x","inbound_addr":%q}`, inbound)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing agentstream_addr, got %d", w.Code)
	}
}

// ---- handleStop ----

func TestServeHTTP_Stop_Valid(t *testing.T) {
	p := newTestProxy(t)
	body := buildStartBody(t, "stop-call-1")
	startReq := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	startW := httptest.NewRecorder()
	p.ServeHTTP(startW, startReq)
	if startW.Code != http.StatusOK {
		t.Fatalf("start failed: %d %s", startW.Code, startW.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id=stop-call-1", nil)
	stopW := httptest.NewRecorder()
	p.ServeHTTP(stopW, stopReq)
	if stopW.Code != http.StatusOK {
		t.Errorf("expected 200 on stop, got %d: %s", stopW.Code, stopW.Body.String())
	}
	if p.ActiveSessions() != 0 {
		t.Errorf("expected 0 sessions after stop, got %d", p.ActiveSessions())
	}
}

func TestServeHTTP_Stop_ViaJSONBody(t *testing.T) {
	p := newTestProxy(t)
	body := buildStartBody(t, "stop-body-call")
	startReq := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
	p.ServeHTTP(httptest.NewRecorder(), startReq)

	stopBody := `{"call_id":"stop-body-call"}`
	stopReq := httptest.NewRequest(http.MethodPost, "/sip/session/stop", strings.NewReader(stopBody))
	stopW := httptest.NewRecorder()
	p.ServeHTTP(stopW, stopReq)
	if stopW.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", stopW.Code, stopW.Body.String())
	}
}

func TestServeHTTP_Stop_UnknownSession(t *testing.T) {
	p := newTestProxy(t)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id=nonexistent", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown session, got %d", w.Code)
	}
}

func TestServeHTTP_Stop_EmptyCallID(t *testing.T) {
	p := newTestProxy(t)
	req := httptest.NewRequest(http.MethodPost, "/sip/session/stop", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for empty call_id, got %d", w.Code)
	}
}

// ---- BandMode tests ----

func TestBandMode_Opus(t *testing.T) {
	sdp := "m=audio 5006 RTP/AVP 111\r\na=rtpmap:111 opus/48000/2\r\n"
	m := ParseSDP(sdp)
	if m.BandMode() != audio.BandFull {
		t.Errorf("expected BandFull for Opus, got %v", m.BandMode())
	}
}

func TestBandMode_G729(t *testing.T) {
	sdp := "m=audio 5012 RTP/AVP 18\r\n"
	m := ParseSDP(sdp)
	if m.BandMode() != audio.BandNarrow {
		t.Errorf("expected BandNarrow for G729, got %v", m.BandMode())
	}
}

func TestBandMode_G722(t *testing.T) {
	sdp := "m=audio 5008 RTP/AVP 9\r\n"
	m := ParseSDP(sdp)
	if m.BandMode() != audio.BandWide {
		t.Errorf("expected BandWide for G722, got %v", m.BandMode())
	}
}

func TestBandMode_PCMU(t *testing.T) {
	sdp := "m=audio 5004 RTP/AVP 0\r\n"
	m := ParseSDP(sdp)
	if m.BandMode() != audio.BandNarrow {
		t.Errorf("expected BandNarrow for PCMU, got %v", m.BandMode())
	}
}

func TestBandMode_PCMA(t *testing.T) {
	sdp := "m=audio 5010 RTP/AVP 8\r\n"
	m := ParseSDP(sdp)
	if m.BandMode() != audio.BandNarrow {
		t.Errorf("expected BandNarrow for PCMA, got %v", m.BandMode())
	}
}

func TestBandMode_Unknown(t *testing.T) {
	m := SDPMedia{Codec: audio.Codec("unknown-codec")}
	if m.BandMode() != audio.BandNarrow {
		t.Errorf("expected BandNarrow (safe default) for unknown codec, got %v", m.BandMode())
	}
}

func TestBandMode_GSM(t *testing.T) {
	m := SDPMedia{Codec: audio.CodecGSM}
	if m.BandMode() != audio.BandNarrow {
		t.Errorf("expected BandNarrow for GSM, got %v", m.BandMode())
	}
}

func TestBandMode_iLBC(t *testing.T) {
	m := SDPMedia{Codec: audio.CodecILBC}
	if m.BandMode() != audio.BandNarrow {
		t.Errorf("expected BandNarrow for iLBC, got %v", m.BandMode())
	}
}

func TestBandMode_Speex(t *testing.T) {
	m := SDPMedia{Codec: audio.CodecSpeex}
	if m.BandMode() != audio.BandWide {
		t.Errorf("expected BandWide for Speex, got %v", m.BandMode())
	}
}

func TestNormalizeSDPCodec_AllCases(t *testing.T) {
	cases := []struct {
		input string
		want  audio.Codec
	}{
		{"PCMU", audio.CodecG711U},
		{"pcmu", audio.CodecG711U},
		{"PCMA", audio.CodecG711A},
		{"pcma", audio.CodecG711A},
		{"G722", audio.CodecG722},
		{"g722", audio.CodecG722},
		{"G729", audio.CodecG729},
		{"g729", audio.CodecG729},
		{"OPUS", audio.CodecOpus},
		{"opus", audio.CodecOpus},
		{"unknown", audio.CodecG711U},
		{"", audio.CodecG711U},
	}
	for _, tc := range cases {
		got := normalizeSDPCodec(tc.input)
		if got != tc.want {
			t.Errorf("normalizeSDPCodec(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---- Concurrent access (race detector) ----

func TestProxy_ConcurrentStartStop(t *testing.T) {
	p := newTestProxy(t)
	const n = 5
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer func() { done <- struct{}{} }()
			callID := fmt.Sprintf("race-call-%d", i)
			body := buildStartBody(t, callID)
			startReq := httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body))
			p.ServeHTTP(httptest.NewRecorder(), startReq)
			stopReq := httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id="+callID, nil)
			p.ServeHTTP(httptest.NewRecorder(), stopReq)
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

func TestProxy_ConcurrentList(t *testing.T) {
	p := newTestProxy(t)
	body := buildStartBody(t, "list-race-call")
	p.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/sip/session/start", strings.NewReader(body)))
	defer func() {
		p.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/sip/session/stop?call_id=list-race-call", nil))
	}()

	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			req := httptest.NewRequest(http.MethodGet, "/sip/sessions", nil)
			p.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
