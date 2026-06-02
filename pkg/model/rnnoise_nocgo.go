//go:build !rnnoise

package model

import (
	"fmt"
	"os"
)

// NewRNNoise returns an error when CGo is not available,
// causing NewSuppressor to fall back to passthrough.
func NewRNNoise() (*Passthrough, error) {
	fmt.Fprintln(os.Stderr, "[clearstream] CGo not available: using passthrough suppressor (no noise reduction)")
	return NewPassthrough(), nil
}
