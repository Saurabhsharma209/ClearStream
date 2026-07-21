//go:build !onnx

package model

import (
	"fmt"

	"go.uber.org/zap"
)

// NewRNNoiseONNX is unavailable without the onnx build tag.
// Build with: CGO_ENABLED=0 go build -tags onnx ./...
func NewRNNoiseONNX(_ string, _ *zap.Logger, _ ...int) (Suppressor, error) {
	return nil, fmt.Errorf("rnnoise-onnx: build with -tags onnx to enable ONNX inference")
}
