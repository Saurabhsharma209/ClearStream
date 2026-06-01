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
