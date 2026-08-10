package model

import (
	"net/http"
	"testing"
)

// TestDeepFilterServerSuppressor_Process_AppliesAggressiveness is a
// regression test for a bug where SuppressorConfig.Aggressiveness was
// silently ignored by the "deepfilter-server" backend: every other backend
// (rnnoise, rnnoise-onnx, deepfilter) calls blendAggressiveness on its output
// (see aggressiveness.go), but deepFilterServerSuppressor.Process returned
// the server's raw response untouched, and newDeepFilterServerSuppressor did
// not even accept an aggressiveness argument. Since deepfilter-server is
// documented as "the primary integration path for DeepFilterNet", any caller
// setting Aggressiveness to 1 or 2 on that backend got full-strength
// suppression instead of the requested blend, with no error or warning.
//
// This test drives the original frame and the server's "enhanced" frame to
// clearly different constant values so the wet/dry blend is unambiguous:
// with level 1 (wet=0.40, dry=0.60) the result must be a blend, not a
// pass-through of either input.
func TestDeepFilterServerSuppressor_Process_AppliesAggressiveness(t *testing.T) {
	frame := []int16{0, 0, 0, 0}                // dry (original) signal
	enhanced := []int16{1000, 1000, 1000, 1000} // wet (model) output
	responsePayload := encodeInt16Slice(enhanced)

	srv := makeTestServer(t, http.StatusOK, http.StatusOK, responsePayload)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger(), 1) // level 1 = mild
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != len(frame) {
		t.Fatalf("Process: got %d samples, want %d", len(out), len(frame))
	}

	// wet=0.40, dry=0.60 -> 1000*0.40 + 0*0.60 = 400 exactly.
	const want = int16(400)
	for i, v := range out {
		if v != want {
			t.Errorf("index %d: got %d, want %d (expected blend of dry=0 and wet=1000 at level 1)", i, v, want)
		}
		// Guard against the pre-fix behavior of passing the raw server
		// output straight through, unblended.
		if v == enhanced[i] {
			t.Errorf("index %d: output %d matches raw server output %d unblended -- Aggressiveness was not applied", i, v, enhanced[i])
		}
	}
}

// TestDeepFilterServerSuppressor_Process_AggressivenessFullStrengthUnchanged
// locks in that level 0 (backend default) and level 3 (aggressive) both
// preserve pre-fix behavior: the server's output passes through unblended,
// matching every caller that never set Aggressiveness on this backend.
func TestDeepFilterServerSuppressor_Process_AggressivenessFullStrengthUnchanged(t *testing.T) {
	frame := []int16{0, 0, 0, 0}
	enhanced := []int16{1000, 1000, 1000, 1000}
	responsePayload := encodeInt16Slice(enhanced)

	for _, level := range []int{0, 3} {
		srv := makeTestServer(t, http.StatusOK, http.StatusOK, responsePayload)

		s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger(), level)
		if err != nil {
			srv.Close()
			t.Fatalf("level %d: setup: %v", level, err)
		}

		out, err := s.Process(frame)
		if err != nil {
			t.Fatalf("level %d: Process: %v", level, err)
		}
		for i, v := range out {
			if v != enhanced[i] {
				t.Errorf("level %d, index %d: got %d, want unblended %d", level, i, v, enhanced[i])
			}
		}

		s.Close()
		srv.Close()
	}
}
