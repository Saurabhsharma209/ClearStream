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
// aggressiveness is variadic for signature parity with the real "rnnoise"
// build (see rnnoise.go) and to preserve compatibility with existing
// no-argument callers; it is intentionally ignored here since Passthrough
// does no suppression at all, so there is no suppression strength to tune.
func NewRNNoise(aggressiveness ...int) (*Passthrough, error) {
	_ = aggressiveness
	warnOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "[clearstream] rnnoise not built in: using passthrough suppressor (build with -tags rnnoise for real noise reduction)")
	})
	return NewPassthrough(), nil
}
