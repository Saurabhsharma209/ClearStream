package model

/*
DeepFilterNet integration via ONNX Runtime Go bindings.

Setup:
  1. Export the DeepFilterNet model to ONNX:
       pip install deepfilternet
       python -c "from df.enhance import init_df; m,_,_ = init_df(); m.export_onnx('deepfilter.onnx')"
  2. Install ONNX Runtime:
       https://github.com/microsoft/onnxruntime/releases
  3. Add Go binding:
       go get github.com/yalue/onnxruntime_go

This file provides the interface binding. The actual onnxruntime_go import
is gated so the SDK compiles without ONNX Runtime installed (falls back to RNNoise).
*/

import (
	"fmt"
	"sync"
)

// DeepFilter wraps a DeepFilterNet ONNX model for high-quality suppression.
// Quality is noticeably better than RNNoise, especially for music and complex noise.
// Requires ~20ms on a modern CPU per 20ms frame (real-time capable).
type DeepFilter struct {
	mu        sync.Mutex
	modelPath string
	// session onnxruntime_go.Session  // uncomment when ONNX Runtime is installed
	// inputName  string
	// outputName string
	sampleRate int
	frameSize  int
}

// NewDeepFilter loads a DeepFilterNet ONNX model from modelPath.
func NewDeepFilter(modelPath string) (*DeepFilter, error) {
	// TODO: Initialize ONNX Runtime session.
	// Uncomment and complete when onnxruntime_go is integrated:
	//
	// ort.InitializeEnvironment()
	// session, err := ort.NewSession(modelPath, inputNames, outputNames, inputShapes, outputShapes)
	// if err != nil {
	//     return nil, fmt.Errorf("deepfilter: load model %q: %w", modelPath, err)
	// }
	//
	// For now, return a stub that logs a warning and passes audio through.
	// Replace with real ONNX session above.

	fmt.Printf("[clearstream] WARNING: DeepFilterNet ONNX Runtime not compiled in. "+
		"Model path %q noted but suppression is passthrough. "+
		"See pkg/model/deepfilter.go for integration instructions.\n", modelPath)

	return &DeepFilter{
		modelPath:  modelPath,
		sampleRate: 48000,
		frameSize:  480, // 10ms @ 48kHz
	}, nil
}

// Process runs the DeepFilterNet inference on a 16kHz 160-sample frame.
// Currently a passthrough stub — replace with real ONNX inference.
func (d *DeepFilter) Process(frame []int16) ([]int16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// TODO: Replace with actual ONNX inference:
	// 1. Resample frame to 48kHz
	// 2. Convert to float32, normalize to [-1, 1]
	// 3. Run ONNX session
	// 4. Convert output back to int16 @ 16kHz

	// Passthrough until ONNX Runtime is wired in
	out := make([]int16, len(frame))
	copy(out, frame)
	return out, nil
}

// Reset clears any stateful buffers in the model.
func (d *DeepFilter) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Reset ONNX session state if applicable
}

// Close releases the ONNX session.
func (d *DeepFilter) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// session.Destroy()
	return nil
}

// Name returns the backend identifier.
func (d *DeepFilter) Name() string { return "deepfilter" }
