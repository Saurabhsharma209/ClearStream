//go:build !onnx

package model

import "fmt"

func newDeepFilterSuppressor(modelPath string, _ interface{}) (Suppressor, error) {
	return nil, fmt.Errorf(
		"deepfilter: SDK built without ONNX support.\n" +
			"Rebuild with: CGO_ENABLED=1 go build -tags onnx ./...\n" +
			"Also requires: github.com/yalue/onnxruntime_go and ONNX Runtime shared lib",
	)
}
