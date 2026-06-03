// Package clearstream whitebox tests — access unexported fields.
package clearstream

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// TestPoolSize_NilPool exercises the nil pool branch of PoolSize().
func TestPoolSize_NilPool(t *testing.T) {
	cs := &ClearStream{pool: nil}
	if got := cs.PoolSize(); got != 0 {
		t.Errorf("PoolSize() with nil pool = %d, want 0", got)
	}
}

// TestClose_NilPool exercises Close() when pool is nil (model only).
func TestClose_NilPool(t *testing.T) {
	logger, _ := zap.NewProduction()
	sup := model.NewPassthrough()
	cs := &ClearStream{
		cfg:    DefaultConfig(),
		model:  sup,
		pool:   nil,
		logger: logger,
	}
	if err := cs.Close(); err != nil {
		t.Errorf("Close() with nil pool returned error: %v", err)
	}
}
