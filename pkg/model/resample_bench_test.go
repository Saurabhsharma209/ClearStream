//go:build rnnoise || onnx

package model

import (
	"math"
	"testing"
)

// resample.go's upsample3x/downsample3x run on every 10ms frame in both the
// CGo RNNoise backend ("rnnoise" tag) and the ONNX backends ("onnx" tag) to
// bridge ClearStream's native 16kHz pipeline to/from the 48kHz rate those
// models expect. Despite being the exact per-frame hot path shared by both
// backends, and despite bench_test.go already benchmarking the RNNoise
// suppressor end-to-end (BenchmarkRNNoiseFrameLatency) and the DeepFilterNet
// ONNX suppressor end-to-end (deepfilter_onnx_bench_test.go), the resampling
// primitives themselves had no dedicated benchmark -- so a regression in the
// Catmull-Rom interpolation or the 15-tap Kaiser-sinc FIR (e.g. accidentally
// reintroducing an allocation per call, or a slower filter) would only show
// up as noise in a much larger end-to-end number, if at all under the
// default (non-rnnoise/onnx) build used by most CI runs.

// BenchmarkUpsample3x measures upsample3x's per-frame Catmull-Rom cubic
// interpolation cost converting one 10ms 16kHz frame (160 samples) to 48kHz
// (480 samples).
func BenchmarkUpsample3x(b *testing.B) {
	in := make([]int16, 160)
	for i := range in {
		in[i] = int16(math.Sin(float64(i)*0.1) * 16000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		upsample3x(in)
	}
}

// BenchmarkDownsample3x measures downsample3x's per-frame 15-tap
// Kaiser-windowed-sinc FIR + decimation cost converting one 48kHz frame (480
// samples) back to 16kHz (160 samples).
func BenchmarkDownsample3x(b *testing.B) {
	in := make([]int16, 480)
	for i := range in {
		in[i] = int16(math.Sin(float64(i)*0.1) * 16000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsample3x(in)
	}
}

// BenchmarkUpsampleDownsampleRoundtrip measures the combined per-frame cost
// of both resampling stages back-to-back, approximating the actual overhead
// resample.go adds to every RNNoise/ONNX Process() call beyond the model
// inference itself.
func BenchmarkUpsampleDownsampleRoundtrip(b *testing.B) {
	in := make([]int16, 160)
	for i := range in {
		in[i] = int16(math.Sin(float64(i)*0.1) * 16000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsample3x(upsample3x(in))
	}
}
