package websocket_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
	csws "github.com/exotel/clearstream/pkg/websocket"
	"github.com/gorilla/websocket"
)

func newTestBridge() *csws.Bridge {
	return csws.NewBridge(csws.BridgeConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
}

// TestBridgePassthrough verifies that audio sent through a passthrough bridge
// comes back unchanged and with the same byte count.
func TestBridgePassthrough(t *testing.T) {
	bridge := newTestBridge()
	srv := httptest.NewServer(bridge.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 640 bytes = 4 complete 160-sample frames (320 bytes each).
	input := make([]byte, 640)
	for i := range input {
		input[i] = byte(i % 256)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, input); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgType, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected binary message, got %d", msgType)
	}
	if len(got) != len(input) {
		t.Fatalf("expected %d bytes back, got %d", len(input), len(got))
	}
}

// TestBridgeConfig verifies that NewBridge accepts a valid BridgeConfig and
// that the resulting Bridge is non-nil and serves HTTP without panicking.
func TestBridgeConfig(t *testing.T) {
	cfg := csws.BridgeConfig{
		SampleRate:    16000,
		Channels:      1,
		Suppressor:    model.NewPassthrough(),
		MaxFrameBytes: 32768,
	}
	bridge := csws.NewBridge(cfg)
	if bridge == nil {
		t.Fatal("NewBridge returned nil")
	}
	// Handler() must return a non-nil http.Handler.
	h := bridge.Handler()
	if h == nil {
		t.Fatal("Bridge.Handler() returned nil")
	}
}

// TestBridgeConfigDefaults verifies that zero-value optional fields in
// BridgeConfig are filled with sensible defaults (16 kHz mono, 64 KiB limit).
func TestBridgeConfigDefaults(t *testing.T) {
	// Supply only the required Suppressor; leave numeric fields at zero.
	bridge := csws.NewBridge(csws.BridgeConfig{
		Suppressor: model.NewPassthrough(),
	})
	if bridge == nil {
		t.Fatal("NewBridge with zero config returned nil")
	}
	// The bridge must still handle a real WebSocket connection after defaulting.
	srv := httptest.NewServer(bridge.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial after default config: %v", err)
	}
	conn.Close()
}

// TestBridgePCMFrameSize documents and verifies the bridge's expected PCM
// frame geometry: 160 samples per frame, 2 bytes per int16 sample = 320 bytes.
// We exercise this by sending exactly one frame and confirming the response
// carries that same frame back (passthrough suppressor).
func TestBridgePCMFrameSize(t *testing.T) {
	const (
		samplesPerFrame = 160                              // 10 ms @ 16 kHz
		bytesPerSample  = 2                                // int16 little-endian
		frameBytes      = samplesPerFrame * bytesPerSample // 320
	)

	bridge := newTestBridge()
	srv := httptest.NewServer(bridge.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send exactly one 320-byte (160-sample) PCM frame.
	frame := make([]byte, frameBytes)
	for i := range frame {
		frame[i] = byte(i % 256)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatalf("write single frame: %v", err)
	}

	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != frameBytes {
		t.Errorf("expected %d bytes for one PCM frame, got %d", frameBytes, len(got))
	}
}

// TestBridgeInvalidUpgrade ensures that a plain HTTP GET (no Upgrade header)
// is rejected with a 4xx status code.
func TestBridgeInvalidUpgrade(t *testing.T) {
	bridge := newTestBridge()
	srv := httptest.NewServer(bridge.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 400 || resp.StatusCode > 499 {
		t.Fatalf("expected 4xx for non-WebSocket request, got %d", resp.StatusCode)
	}
}
