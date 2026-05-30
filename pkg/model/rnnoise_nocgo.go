//go:build !cgo

package model

import "fmt"

// NewRNNoise returns an error when CGo is not available,
// causing NewSuppressor to fall back to passthrough.
func NewRNNoise() (*Passthrough, error) {
	fmt.Println("[clearstream] CGo not available: using passthrough suppressor (no noise reduction)")
	return NewPassthrough(), nil
}
