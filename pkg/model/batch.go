package model

// BatchWrapper wraps any Suppressor and implements BatchSuppressor by
// calling Process sequentially. This is the default fallback; backends
// can implement ProcessBatch directly for SIMD-optimised throughput.
type BatchWrapper struct {
	s Suppressor
}

// Process implements Suppressor by delegating to the wrapped suppressor.
func (b *BatchWrapper) Process(frame []int16) ([]int16, error) {
	return b.s.Process(frame)
}

// Reset implements Suppressor.
func (b *BatchWrapper) Reset() { b.s.Reset() }

// Close implements Suppressor.
func (b *BatchWrapper) Close() error { return b.s.Close() }

// Name implements Suppressor.
func (b *BatchWrapper) Name() string { return b.s.Name() + "+batch" }

// ProcessBatch processes frames sequentially. Stops and returns on first error,
// appending already-processed frames plus the remaining unprocessed frames.
func (b *BatchWrapper) ProcessBatch(frames [][]int16) ([][]int16, error) {
	out := make([][]int16, len(frames))
	for i, f := range frames {
		processed, err := b.s.Process(f)
		if err != nil {
			// return processed so far + remaining originals
			for j := i; j < len(frames); j++ {
				out[j] = frames[j]
			}
			return out, err
		}
		out[i] = processed
	}
	return out, nil
}
