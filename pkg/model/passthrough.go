package model

// Passthrough is a no-op Suppressor useful for testing pipelines without
// running an actual AI model. Audio passes through unchanged.
type Passthrough struct{}

// NewPassthrough returns a Passthrough suppressor.
func NewPassthrough() *Passthrough { return &Passthrough{} }

// Process returns the input frame directly (zero-copy).
// Callers must not mutate the returned slice after calling Process,
// as it aliases the input buffer.
func (p *Passthrough) Process(frame []int16) ([]int16, error) {
	return frame, nil
}

// Reset is a no-op for Passthrough; there is no internal state to clear.
func (p *Passthrough) Reset() {}

// Close is a no-op for Passthrough; no resources are held.
func (p *Passthrough) Close() error { return nil }

// Name returns the backend identifier "passthrough".
func (p *Passthrough) Name() string { return "passthrough" }

// ProcessBatch implements BatchSuppressor - passes all frames through unchanged (zero-copy).
func (p *Passthrough) ProcessBatch(frames [][]int16) ([][]int16, error) {
	return frames, nil
}
