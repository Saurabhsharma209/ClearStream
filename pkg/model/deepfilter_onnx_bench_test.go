//go:build onnx

package model

// Benchmarks for the deepFilterSuppressor lifecycle (create/process/reset/close),
// mirroring the mock-session unit tests in deepfilter_onnx_test.go and the
// benchmark style used in bench_test.go (BenchmarkRNNoiseFrameLatency,
// BenchmarkPassthrough, BenchmarkMockSuppressor).
//
// These benchmarks use mockONNXSession / mockDeepFilterSuppressor (defined in
// deepfilter_onnx_test.go) so they run without a real ONNX Runtime shared
// library or exported .onnx model file.
//
// Run: CGO_ENABLED=1 go test -tags onnx -bench=DeepFilter -benchmem ./pkg/model/...

import "testing"

// BenchmarkDeepFilterSuppressorProcess measures steady-state Process() latency
// and allocations for a single 480-sample frame (10ms @ 48kHz, DeepFilterNet's
// native rate), matching the frame size used in
// TestDeepFilterMockSessionLifecycle.
func BenchmarkDeepFilterSuppressorProcess(b *testing.B) {
	sup := &mockDeepFilterSuppressor{session: &mockONNXSession{}}
	defer sup.Close()

	frame := make([]int16, 480)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sup.Process(frame); err != nil {
			b.Fatalf("Process() error: %v", err)
		}
	}
}

// BenchmarkDeepFilterSuppressorLifecycle measures the full create -> process
// (N frames) -> reset -> close lifecycle per iteration, giving a per-call-cycle
// cost estimate analogous to BenchmarkRNNoiseFrameLatency.
func BenchmarkDeepFilterSuppressorLifecycle(b *testing.B) {
	frame := make([]int16, 480)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}
	const framesPerIteration = 10

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// create
		sup := &mockDeepFilterSuppressor{session: &mockONNXSession{}}

		// process N frames
		for f := 0; f < framesPerIteration; f++ {
			if _, err := sup.Process(frame); err != nil {
				b.Fatalf("Process() error: %v", err)
			}
		}

		// reset
		sup.Reset()

		// process once more after reset
		if _, err := sup.Process(frame); err != nil {
			b.Fatalf("Process() after Reset() error: %v", err)
		}

		// close
		if err := sup.Close(); err != nil {
			b.Fatalf("Close() error: %v", err)
		}
	}
}
