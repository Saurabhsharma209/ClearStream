package model

// Passthrough is a no-op Suppressor useful for testing pipelines without
// running an actual AI model. Audio passes through unchanged.
type Passthrough struct{}

// NewPassthrough returns a Passthrough suppressor.
func NewPassthrough() *Passthrough { return &Passthrough{} }

// Process copies the input frame unchanged and returns it.
func (p *Passthrough) Process(frame []int16) ([]int16, error) {
	out := make([]int16, len(frame))
	copy(out, frame)
	return out, nil
}

// Reset is a no-op for Passthrough; there is no internal state to clear.
func (p *Passthrough) Reset() {}

// Close is a no-op for Passthrough; no resources are held.
func (p *Passthrough) Close() error { return nil }

// Name returns the backend identifier "passthrough".
func (p *Passthrough) Name() string { return "passthrough" }
