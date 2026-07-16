// Package http_test covers POST /enhance/dir, the batch/directory equivalent
// of POST /enhance -- added to close the gap where the CLI's `dir`
// subcommand supported batch directory processing (with worker-count and
// skip-existing controls) but the HTTP API only ever exposed single-file
// enhancement.
package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cshttp "github.com/exotel/clearstream/pkg/http"
)

func postEnhanceDir(t *testing.T, h *cshttp.Handler, reqBody interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	buf, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/enhance/dir", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var decoded map[string]interface{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode response body %q: %v", w.Body.String(), err)
		}
	}
	return w, decoded
}

func TestEnhanceDirMissingFields(t *testing.T) {
	h := newTestHandler()
	w, _ := postEnhanceDir(t, h, map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing input_dir/output_dir, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestEnhanceDirInvalidJSON(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/enhance/dir", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestEnhanceDirNonExistentInput(t *testing.T) {
	h := newTestHandler()
	outDir := t.TempDir()
	w, resp := postEnhanceDir(t, h, map[string]string{
		"input_dir":  filepath.Join(t.TempDir(), "does-not-exist"),
		"output_dir": outDir,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (batch endpoint reports failures in-band), got %d; body: %s", w.Code, w.Body.String())
	}
	if failed, _ := resp["failed"].(float64); failed < 1 {
		t.Errorf("expected at least 1 failed entry for non-existent input_dir, got response: %+v", resp)
	}
}

func TestEnhanceDirProcessesFiles(t *testing.T) {
	h := newTestHandler()
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "out")

	wavData := buildWAVBytes(make([]int16, 1600))
	if err := os.WriteFile(filepath.Join(srcDir, "call.wav"), wavData, 0644); err != nil {
		t.Fatalf("write test wav: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "notes.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write test txt: %v", err)
	}

	w, resp := postEnhanceDir(t, h, map[string]interface{}{
		"input_dir":  srcDir,
		"output_dir": dstDir,
		"workers":    2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	skipped, _ := resp["skipped"].(float64)
	if skipped < 1 {
		t.Errorf("expected notes.txt to be reported as skipped (unsupported ext), got response: %+v", resp)
	}

	processed, _ := resp["processed"].(float64)
	failed, _ := resp["failed"].(float64)
	if processed < 1 && failed < 1 {
		t.Fatalf("expected call.wav to be reported as processed or failed, got response: %+v", resp)
	}
	if processed >= 1 {
		if _, err := os.Stat(filepath.Join(dstDir, "call.wav")); err != nil {
			t.Errorf("expected enhanced output file to exist at %s: %v", filepath.Join(dstDir, "call.wav"), err)
		}
	}

	files, ok := resp["files"].([]interface{})
	if !ok || len(files) != 2 {
		t.Errorf("expected 2 file entries in response, got: %+v", resp["files"])
	}
}

func TestEnhanceDirSkipExisting(t *testing.T) {
	h := newTestHandler()
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	wavData := buildWAVBytes(make([]int16, 1600))
	srcPath := filepath.Join(srcDir, "call.wav")
	dstPath := filepath.Join(dstDir, "call.wav")
	if err := os.WriteFile(srcPath, wavData, 0644); err != nil {
		t.Fatalf("write src wav: %v", err)
	}
	if err := os.WriteFile(dstPath, wavData, 0644); err != nil {
		t.Fatalf("write dst wav: %v", err)
	}

	w, resp := postEnhanceDir(t, h, map[string]interface{}{
		"input_dir":     srcDir,
		"output_dir":    dstDir,
		"skip_existing": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	skipped, _ := resp["skipped"].(float64)
	if skipped < 1 {
		t.Errorf("expected call.wav to be skipped as already-processed, got response: %+v", resp)
	}
}
