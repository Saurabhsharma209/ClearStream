package audio

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

// TestTieredNR_ResetDelegatesToConfiguredSuppressors verifies that Reset
// actually forwards to the RNNoise and DeepFilter sub-suppressors when they
// are configured, not just to the internal gate. TestTieredNR_Reset (in
// tiered_nr_test.go) only exercises DefaultTieredNRConfig, which leaves
// both RNNoise and DeepFilter nil, so the "if t.rnnoise != nil" /
// "if t.deepfilter != nil" branches inside Reset were never covered by any
// test -- a regression here (e.g. an accidentally removed nil-guard call)
// would have gone unnoticed.
func TestTieredNR_ResetDelegatesToConfiguredSuppressors(t *testing.T) {
	rnnoise := model.NewMockSuppressor()
	deepfilter := model.NewMockSuppressor()
	cfg := DefaultTieredNRConfig()
	cfg.RNNoise = rnnoise
	cfg.DeepFilter = deepfilter
	nr := NewTieredNR(cfg)

	nr.Reset()

	if rnnoise.ResetCalls != 1 {
		t.Errorf("expected RNNoise.Reset() called once, got %d calls", rnnoise.ResetCalls)
	}
	if deepfilter.ResetCalls != 1 {
		t.Errorf("expected DeepFilter.Reset() called once, got %d calls", deepfilter.ResetCalls)
	}
	if nr.noiseFloor != 1.0 {
		t.Fatalf("noiseFloor not reset: got %f", nr.noiseFloor)
	}
}
