//go:build onnx

package model

import (
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
	"go.uber.org/zap"
)

// deepFilterSuppressor runs DeepFilterNet inference via ONNX Runtime.
// Build with: CGO_ENABLED=1 go build -tags onnx ./...
// Export model: python -c "from df.enhance import init_df; m,_,_=init_df(); m.export_onnx('deepfilter.onnx')"
type deepFilterSuppressor struct {
	mu        sync.Mutex
	session   *ort.DynamicAdvancedSession
	modelPath string
	logger    *zap.Logger
}

func newDeepFilterSuppressor(modelPath string, logger *zap.Logger) (Suppressor, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("deepfilter: ModelPath is required")
	}

	// Initialize ONNX Runtime (idempotent)
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("deepfilter: init onnx env: %w", err)
		}
	}

	// Load model — use DynamicAdvancedSession for variable input shapes
	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{"input"},  // DeepFilterNet input node name
		[]string{"output"}, // DeepFilterNet output node name
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("deepfilter: load model %q: %w", modelPath, err)
	}

	logger.Info("DeepFilterNet model loaded", zap.String("path", modelPath))
	return &deepFilterSuppressor{session: session, modelPath: modelPath, logger: logger}, nil
}

// Process suppresses noise in a 16kHz mono int16 PCM frame using DeepFilterNet.
func (d *deepFilterSuppressor) Process(frame []int16) ([]int16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Convert int16 -> float32 normalized [-1, 1]
	floats := make([]float32, len(frame))
	for i, s := range frame {
		floats[i] = float32(s) / 32768.0
	}

	// Create input tensor
	inputTensor, err := ort.NewTensor(ort.NewShape(1, int64(len(floats))), floats)
	if err != nil {
		return frame, fmt.Errorf("deepfilter: input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	// Run inference
	outputs, err := d.session.Run([]ort.ArbitraryTensor{inputTensor})
	if err != nil {
		// Graceful degradation: return original frame on error
		d.logger.Warn("deepfilter inference failed, passing through", zap.Error(err))
		return frame, nil
	}
	defer func() {
		for _, o := range outputs {
			o.Destroy()
		}
	}()

	// Convert output float32 -> int16
	outTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return frame, fmt.Errorf("deepfilter: unexpected output type")
	}
	outData := outTensor.GetData()

	result := make([]int16, len(outData))
	for i, f := range outData {
		if f > 1.0 {
			f = 1.0
		}
		if f < -1.0 {
			f = -1.0
		}
		result[i] = int16(f * 32767)
	}
	return result, nil
}

func (d *deepFilterSuppressor) Name() string { return "deepfilter" }
func (d *deepFilterSuppressor) Reset()       {} // stateless per-frame

func (d *deepFilterSuppressor) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session != nil {
		d.session.Destroy()
		d.session = nil
	}
	return nil
}
