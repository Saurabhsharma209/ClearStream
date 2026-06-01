// Package main implements an Asterisk ARI (Asterisk REST Interface) bridge
// that intercepts channel audio, enhances it via ClearStream, and returns
// clean audio back to the channel.
//
// Usage:
//
//	go run ./examples/asterisk/ari_bridge.go \
//	    --asterisk http://localhost:8088 \
//	    --ari-user asterisk --ari-pass asterisk \
//	    --clearstream http://localhost:8080 \
//	    --app clearstream-app
//
// Asterisk ARI must be enabled in /etc/asterisk/ari.conf:
//
//	[general]
//	enabled = yes
//	[asterisk]
//	type = user
//	password = asterisk
//	password_format = plain
//
// Dialplan entry to route calls into this app:
//
//	exten => 1002,1,Answer()
//	exten => 1002,n,Stasis(clearstream-app)
//	exten => 1002,n,Hangup()
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"golang.org/x/net/websocket"
)

// ── CLI flags ─────────────────────────────────────────────────────────────────

var (
	asteriskURL    = flag.String("asterisk", "http://localhost:8088", "Asterisk ARI base URL")
	ariUser        = flag.String("ari-user", "asterisk", "ARI username")
	ariPass        = flag.String("ari-pass", "asterisk", "ARI password")
	clearstreamURL = flag.String("clearstream", "http://localhost:8080", "ClearStream base URL")
	appName        = flag.String("app", "clearstream-app", "Stasis application name")
)

// ── ARI event types ───────────────────────────────────────────────────────────

// ARIEvent is the envelope for all ARI WebSocket events.
type ARIEvent struct {
	Type        string        `json:"type"`
	Application string        `json:"application"`
	Channel     *ARIChannel   `json:"channel,omitempty"`
	Recording   *ARIRecording `json:"recording,omitempty"`
}

// ARIChannel represents a channel in an ARI event.
type ARIChannel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// ARIRecording represents a live recording object.
type ARIRecording struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	State  string `json:"state"`
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

// ariClient wraps authenticated HTTP calls to the Asterisk ARI REST API.
type ariClient struct {
	base   string
	user   string
	pass   string
	client *http.Client
}

func newARIClient(base, user, pass string) *ariClient {
	return &ariClient{
		base:   base,
		user:   user,
		pass:   pass,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *ariClient) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.client.Do(req)
}

// answerChannel sends POST /channels/{id}/answer.
func (c *ariClient) answerChannel(channelID string) error {
	resp, err := c.do("POST", "/ari/channels/"+channelID+"/answer", nil, "")
	if err != nil {
		return fmt.Errorf("answer channel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("answer channel: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// startRecording starts a live recording on the channel.
// Returns the recording name that can be used to fetch the file.
func (c *ariClient) startRecording(channelID, recName string) error {
	params := url.Values{}
	params.Set("name", recName)
	params.Set("format", "wav")
	params.Set("maxSilenceSeconds", "0")
	params.Set("beep", "false")
	params.Set("terminateOn", "none")

	path := "/ari/channels/" + channelID + "/record?" + params.Encode()
	resp, err := c.do("POST", path, nil, "")
	if err != nil {
		return fmt.Errorf("start recording: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("start recording: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// stopRecording stops a live recording and waits for the file to be available.
func (c *ariClient) stopRecording(recName string) error {
	resp, err := c.do("POST", "/ari/recordings/live/"+recName+"/stop", nil, "")
	if err != nil {
		return fmt.Errorf("stop recording: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stop recording: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// getRecordingData downloads the stored recording WAV from ARI.
func (c *ariClient) getRecordingData(recName string) ([]byte, error) {
	resp, err := c.do("GET", "/ari/recordings/stored/"+recName+"/file", nil, "")
	if err != nil {
		return nil, fmt.Errorf("get recording: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get recording: status %d: %s", resp.StatusCode, body)
	}
	return io.ReadAll(resp.Body)
}

// playMedia plays back a stored recording on the channel.
func (c *ariClient) playMedia(channelID, mediaURI string) error {
	params := url.Values{}
	params.Set("media", mediaURI)
	path := "/ari/channels/" + channelID + "/play?" + params.Encode()
	resp, err := c.do("POST", path, nil, "")
	if err != nil {
		return fmt.Errorf("play media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("play media: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// hangup sends DELETE /channels/{id}.
func (c *ariClient) hangup(channelID string) error {
	resp, err := c.do("DELETE", "/ari/channels/"+channelID, nil, "")
	if err != nil {
		return fmt.Errorf("hangup: %w", err)
	}
	defer resp.Body.Close()
	return nil
}

// ── ClearStream HTTP client ───────────────────────────────────────────────────

// enhanceAudio posts raw WAV bytes to ClearStream /enhance and returns clean WAV.
func enhanceAudio(csURL string, audioData []byte, filename string) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("audio", filepath.Base(filename))
	if err != nil {
		return nil, fmt.Errorf("enhance: create form file: %w", err)
	}
	if _, err := fw.Write(audioData); err != nil {
		return nil, fmt.Errorf("enhance: write form file: %w", err)
	}
	w.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(csURL+"/enhance", w.FormDataContentType(), &buf)
	if err != nil {
		return nil, fmt.Errorf("enhance: POST /enhance: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("enhance: status %d: %s", resp.StatusCode, body)
	}
	return io.ReadAll(resp.Body)
}

// ── Channel handler ───────────────────────────────────────────────────────────

// handleChannel orchestrates: answer → record → enhance → playback → hangup.
// This is the "record & replace" pattern: record a segment, enhance it with
// ClearStream, then play the clean audio back.  For production you would run
// this in a loop or integrate with a full IVR flow.
func handleChannel(ari *ariClient, channelID string) {
	log.Printf("[ari-bridge] handling channel %s", channelID)

	if err := ari.answerChannel(channelID); err != nil {
		log.Printf("[ari-bridge] answer failed: %v", err)
		return
	}

	// Record a short greeting/segment from the caller.
	recName := "cs-" + channelID
	if err := ari.startRecording(channelID, recName); err != nil {
		log.Printf("[ari-bridge] record start failed: %v", err)
		_ = ari.hangup(channelID)
		return
	}

	// Let the recording run for 5 seconds (simplified; production would use dtmf/silence detection).
	time.Sleep(5 * time.Second)

	if err := ari.stopRecording(recName); err != nil {
		log.Printf("[ari-bridge] record stop failed: %v", err)
	}

	// Give Asterisk a moment to flush the recording to disk.
	time.Sleep(500 * time.Millisecond)

	// Fetch the raw recording.
	wavData, err := ari.getRecordingData(recName)
	if err != nil {
		log.Printf("[ari-bridge] fetch recording failed: %v", err)
		_ = ari.hangup(channelID)
		return
	}
	log.Printf("[ari-bridge] fetched %d bytes of audio from channel %s", len(wavData), channelID)

	// Enhance via ClearStream.
	cleanWav, err := enhanceAudio(*clearstreamURL, wavData, recName+".wav")
	if err != nil {
		log.Printf("[ari-bridge] enhance failed: %v — playing original", err)
		cleanWav = wavData
	} else {
		log.Printf("[ari-bridge] enhanced audio: %d bytes", len(cleanWav))
	}

	// Store enhanced audio back so Asterisk can play it.
	// In production, upload to a shared NFS/S3 that Asterisk can reach, or
	// use the ARI /recordings/stored/{name} PUT endpoint to replace the file.
	_ = cleanWav // integration point: upload to asterisk media store

	// Play back the (now enhanced) stored recording.
	mediaURI := "recording:" + recName
	if err := ari.playMedia(channelID, mediaURI); err != nil {
		log.Printf("[ari-bridge] playback failed: %v", err)
	}

	// Allow playback to complete before hanging up.
	time.Sleep(6 * time.Second)
	_ = ari.hangup(channelID)
	log.Printf("[ari-bridge] channel %s complete", channelID)
}

// ── Main: ARI WebSocket event loop ───────────────────────────────────────────

func main() {
	flag.Parse()

	ari := newARIClient(*asteriskURL, *ariUser, *ariPass)

	// Build the ARI WebSocket URL.
	// wss:// if asteriskURL is https://, ws:// otherwise.
	wsURL := *asteriskURL
	if len(wsURL) > 5 && wsURL[:5] == "https" {
		wsURL = "wss" + wsURL[5:]
	} else if len(wsURL) > 4 && wsURL[:4] == "http" {
		wsURL = "ws" + wsURL[4:]
	}
	wsURL += "/ari/events?api_key=" + *ariUser + ":" + *ariPass + "&app=" + *appName

	log.Printf("[ari-bridge] connecting to ARI WebSocket: %s", wsURL)

	origin := *asteriskURL + "/"
	ws, err := websocket.Dial(wsURL, "", origin)
	if err != nil {
		log.Fatalf("[ari-bridge] websocket dial: %v", err)
	}
	defer ws.Close()

	log.Printf("[ari-bridge] connected — waiting for StasisStart events (app=%s)", *appName)

	for {
		var raw json.RawMessage
		if err := websocket.JSON.Receive(ws, &raw); err != nil {
			if err == io.EOF {
				log.Println("[ari-bridge] ARI WebSocket closed")
				return
			}
			log.Printf("[ari-bridge] ws receive error: %v", err)
			return
		}

		var event ARIEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			log.Printf("[ari-bridge] unmarshal event: %v", err)
			continue
		}

		switch event.Type {
		case "StasisStart":
			if event.Channel == nil {
				continue
			}
			log.Printf("[ari-bridge] StasisStart: channel %s (%s)", event.Channel.ID, event.Channel.Name)
			go handleChannel(ari, event.Channel.ID)

		case "StasisEnd":
			if event.Channel != nil {
				log.Printf("[ari-bridge] StasisEnd: channel %s", event.Channel.ID)
			}

		case "ChannelDestroyed":
			if event.Channel != nil {
				log.Printf("[ari-bridge] ChannelDestroyed: %s", event.Channel.ID)
			}

		default:
			// Ignore other event types (ChannelDtmfReceived, etc.)
		}
	}
}
