package sip

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	rtppkg "github.com/exotel/clearstream/pkg/rtp"
	"go.uber.org/zap"
)

// SessionRequest is the JSON body for POST /session/start.
type SessionRequest struct {
	// SDP is the SIP SDP offer body (used to auto-detect codec).
	SDP string `json:"sdp"`
	// InboundAddr is where we listen for RTP from the SIP trunk (caller audio).
	InboundAddr string `json:"inbound_addr"`
	// AgentStreamAddr is where AgentStream's STT is listening for clean RTP.
	AgentStreamAddr string `json:"agentstream_addr"`
	// OutboundAddr is where we listen for TTS audio from AgentStream.
	OutboundAddr string `json:"outbound_addr"`
	// CallerAddr is where we forward enhanced audio back to the SIP trunk.
	CallerAddr string `json:"caller_addr"`
	// CallID is a unique identifier for this call (from SIP Call-ID header).
	CallID string `json:"call_id"`
}

// SessionResponse is returned by POST /session/start.
type SessionResponse struct {
	CallID  string `json:"call_id"`
	Status  string `json:"status"`
	Codec   string `json:"codec"`
	Message string `json:"message,omitempty"`
}

// ProxySession represents one active call being enhanced.
type ProxySession struct {
	CallID       string
	Codec        audio.Codec
	InboundSess  *rtppkg.Session // caller -> STT (noise suppressed)
	OutboundSess *rtppkg.Session // TTS -> caller (pass-through + light cleanup)
	cancel       context.CancelFunc
}

// Proxy manages multiple concurrent SIP media sessions.
// It exposes an HTTP control API and transparently enhances audio.
type Proxy struct {
	suppressor model.Suppressor
	logger     *zap.Logger

	mu       sync.RWMutex
	sessions map[string]*ProxySession
}

// NewProxy creates a new SIP media proxy.
func NewProxy(sup model.Suppressor, logger *zap.Logger) *Proxy {
	return &Proxy{
		suppressor: sup,
		logger:     logger,
		sessions:   make(map[string]*ProxySession),
	}
}

// ServeHTTP implements http.Handler. Mount this on /sip/ in your HTTP server.
//
// Routes:
//
//	POST /sip/session/start  -- start a new enhanced session
//	POST /sip/session/stop   -- stop a session by call_id
//	GET  /sip/sessions        -- list active sessions
//	GET  /sip/health          -- health check
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/sip/session/start":
		p.handleStart(w, r)
	case "/sip/session/stop":
		p.handleStop(w, r)
	case "/sip/sessions":
		p.handleList(w, r)
	case "/sip/health":
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
}

func (p *Proxy) handleStart(w http.ResponseWriter, r *http.Request) {
	var req SessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.CallID == "" || req.InboundAddr == "" || req.AgentStreamAddr == "" {
		http.Error(w, `{"error":"call_id, inbound_addr, agentstream_addr required"}`, http.StatusBadRequest)
		return
	}

	// Parse SDP to detect codec automatically.
	media := ParseSDP(req.SDP)
	p.logger.Info("starting SIP session",
		zap.String("call_id", req.CallID),
		zap.String("codec", string(media.Codec)),
		zap.Int("payload_type", int(media.PayloadType)),
	)

	// Inbound session: caller -> ClearStream noise suppression -> AgentStream STT.
	inbound, err := rtppkg.NewSession(rtppkg.Config{
		ListenAddr:  req.InboundAddr,
		ForwardAddr: req.AgentStreamAddr,
		Codec:       media.Codec,
		PayloadType: media.PayloadType,
		JitterDepth: 4,
		SampleRate:  16000,
		Suppressor:  p.suppressor,
		Logger:      p.logger,
	})
	if err != nil {
		p.logger.Error("failed to create inbound session", zap.Error(err))
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	sess := &ProxySession{
		CallID:      req.CallID,
		Codec:       media.Codec,
		InboundSess: inbound,
	}

	// Outbound session (TTS -> caller): optional, passthrough with light jitter smoothing.
	if req.OutboundAddr != "" && req.CallerAddr != "" {
		outbound, oErr := rtppkg.NewSession(rtppkg.Config{
			ListenAddr:  req.OutboundAddr,
			ForwardAddr: req.CallerAddr,
			Codec:       media.Codec,
			PayloadType: media.PayloadType,
			JitterDepth: 2,
			SampleRate:  16000,
			Suppressor:  model.NewPassthrough(), // TTS is already clean
			Logger:      p.logger,
		})
		if oErr == nil {
			sess.OutboundSess = outbound
		}
	}

	p.mu.Lock()
	p.sessions[req.CallID] = sess
	p.mu.Unlock()

	sess.InboundSess.Start()
	if sess.OutboundSess != nil {
		sess.OutboundSess.Start()
	}

	json.NewEncoder(w).Encode(SessionResponse{
		CallID:  req.CallID,
		Status:  "started",
		Codec:   string(media.Codec),
		Message: fmt.Sprintf("enhancing %s audio from %s -> %s", media.Codec, req.InboundAddr, req.AgentStreamAddr),
	})
}

func (p *Proxy) handleStop(w http.ResponseWriter, r *http.Request) {
	callID := r.URL.Query().Get("call_id")
	if callID == "" {
		var req struct {
			CallID string `json:"call_id"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		callID = req.CallID
	}

	p.mu.Lock()
	sess, ok := p.sessions[callID]
	if ok {
		delete(p.sessions, callID)
	}
	p.mu.Unlock()

	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	sess.InboundSess.Stop()
	if sess.OutboundSess != nil {
		sess.OutboundSess.Stop()
	}

	json.NewEncoder(w).Encode(map[string]string{
		"call_id": callID,
		"status":  "stopped",
	})
}

func (p *Proxy) handleList(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	type info struct {
		CallID string `json:"call_id"`
		Codec  string `json:"codec"`
	}
	var list []info
	for id, s := range p.sessions {
		list = append(list, info{CallID: id, Codec: string(s.Codec)})
	}
	if list == nil {
		list = []info{}
	}
	json.NewEncoder(w).Encode(list)
}

// ActiveSessions returns the number of currently active sessions.
func (p *Proxy) ActiveSessions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessions)
}
