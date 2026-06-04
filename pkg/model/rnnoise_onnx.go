//go:build onnx

package model

// RNNoise-ONNX suppressor — runs RNNoise inference via ONNX Runtime.
// No CGo required; links only against the ONNX Runtime shared library.
//
// # Build
//
//	CGO_ENABLED=0 go build -tags onnx ./...
//
// # Export the model (Python, one-time)
//
//	pip install rnnoise-wrapper torch
//	python scripts/export_rnnoise_onnx.py --out models/rnnoise.onnx
//
// # Model spec
//
// The ONNX model accepted here follows the standard RNNoise architecture:
//   - Input  "input"  : [1, 480] float32  (48 kHz, 10 ms frame, normalised -1…1)
//   - Output "output" : [1, 480] float32  (denoised, same normalisation)
//
// Because ClearStream operates at 16 kHz internally, each 160-sample frame is
// linearly upsampled to 480 samples before inference and downsampled back after.
//
// # Graceful degradation
//
// If the ONNX session returns an error for any frame, the original frame is
// returned unchanged (no crash). A warning is logged at most once per session.

import (
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
	"go.uber.org/zap"
)

const (
	rnnoiseInputSamples  = 480 // 10 ms @ 48 kHz
	rnnoiseOutputSamples = 480
	rnnoiseNativeSR      = 48000
)

// rnnoiseONNXSuppressor wraps an exported RNNoise ONNX model.
type rnnoiseONNXSuppressor struct {
	mu        sync.Mutex
	session   *ort.DynamicAdvancedSession
	modelPath string
	logger    *zap.Logger
	warnOnce  sync.Once
}

// NewRNNoiseONNX loads the RNNoise ONNX model from modelPath.
func NewRNNoiseONNX(modelPath string, logger *zap.Logger) (Suppressor, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("rnnoise-onnx: ModelPath is required")
	}
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("rnnoise-onnx: init ort env: %w", err)
		}
	}
	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{"input"},
		[]string{"output"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("rnnoise-onnx: load %q: %w", modelPath, err)
	}
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	logger.Info("RNNoise ONNX model loaded", zap.String("path", modelPath))
	return &rnnoiseONNXSuppressor{session: session, modelPath: modelPath, logger: logger}, nil
}

// Process denoises a 16 kHz mono PCM frame via RNNoise ONNX inference.
// Frame must be exactly 160 samples (10 ms). Returns original frame on error.
func (r *rnnoiseONNXSuppressor) Process(frame []int16) ([]int16, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Upsample 160 samples @ 16 kHz → 480 samples @ 48 kHz (3× linear interp).
	up := upsample3x(frame)

	// Normalise to float32 [-1, 1].
	floats := make([]float32, rnnoiseInputSamples)
	for i, s := range up {
		floats[i] = float32(s) / 32768.0
	}

	inputTensor, err := ort.NewTensor(ort.NewShape(1, rnnoiseInputSamples), floats)
	if err != nil {
		r.logWarn("input tensor alloc failed", err)
		return frame, nil
	}
	defer inputTensor.Destroy()

	outputs, err := r.session.Run([]ort.ArbitraryTensor{inputTensor})
	if err != nil {
		r.logWarn("inference failed, passing through", err)
		return frame, nil
	}
	defer func() {
		for _, o := range outputs {
			o.Destroy()
		}
	}()

	outTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		r.logWarn("unexpected output tensor type", nil)
		return frame, nil
	}
	outData := outTensor.GetData()
	if len(outData) < rnnoiseOutputSamples {
		r.logWarn("short output tensor", nil)
		return frame, nil
	}

	// Clip and convert float32 → int16 at 48 kHz.
	up48 := make([]int16, rnnoiseOutputSamples)
	for i, f := range outData[:rnnoiseOutputSamples] {
		if f > 1.0 {
			f = 1.0
		} else if f < -1.0 {
			f = -1.0
		}
		up48[i] = int16(f * 32767)
	}

	// Downsample 480 → 160 samples (3× decimation with simple averaging).
	return downsample3x(up48), nil
}

func (r *rnnoiseONNXSuppressor) Name() string { return "rnnoise-onnx" }
func (r *rnnoiseONNXSuppressor) Reset()       {} // stateless per frame

func (r *rnnoiseONNXSuppressor) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != nil {
		r.session.Destroy()
		r.session = nil
	}
	return nil
}

func (r *rnnoiseONNXSuppressor) logWarn(msg string, err error) {
	r.warnOnce.Do(func() {
		if err != nil {
			r.logger.Warn("rnnoise-onnx: "+msg, zap.Error(err))
		} else {
			r.logger.Warn("rnnoise-onnx: " + msg)
		}
	})
}

// upsample3x linearly interpolates a 16kHz frame to 48kHz (3× rate).
func upsample3x(in []int16) []int16 {
	out := make([]int16, len(in)*3)
	for i, s := range in {
		var next int16
		if i+1 < len(in) {
			next = in[i+1]
		} else {
			next = s
		}
		out[i*3] = s
		out[i*3+1] = int16((int32(s)*2 + int32(next)) / 3)
		out[i*3+2] = int16((int32(s) + int32(next)*2) / 3)
	}
	return out
}

// downsample3x averages every 3 samples to convert 48kHz back to 16kHz.
func downsample3x(in []int16) []int16 {
	out := make([]int16, len(in)/3)
	for i := range out {
		avg := (int32(in[i*3]) + int32(in[i*3+1]) + int32(in[i*3+2])) / 3
		out[i] = int16(avg)
	}
	return out
}
