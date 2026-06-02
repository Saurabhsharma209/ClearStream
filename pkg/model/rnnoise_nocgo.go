//go:build !rnnoise

package model

import (
	"fmt"
	"os"
	"sync"
)

var warnOnce sync.Once

// NewRNNoise returns a passthrough suppressor when rnnoise is not built in.
// A one-time warning is printed to stderr so pool creation doesn't spam logs.
func NewRNNoise() (*Passthrough, error) {
	warnOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "[clearstream] rnnoise not built in: using passthrough suppressor (build with -tags rnnoise for real noise reduction)")
	})
	return NewPassthrough(), nil
}
