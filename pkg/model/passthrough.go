package model

// Passthrough is a no-op Suppressor useful for testing pipelines without
// running an actual AI model. Audio passes through unchanged.
type Passthrough struct{}

// NewPassthrough returns a Passthrough suppressor.
func NewPassthrough() *Passthrough { return &Passthrough{} }

func (p *Passthrough) Process(frame []int16) ([]int16, error) {
	out := make([]int16, len(frame))
	copy(out, frame)
	return out, nil
}

func (p *Passthrough) Reset()        {}
func (p *Passthrough) Close() error  { return nil }
func (p *Passthrough) Name() string  { return "passthrough" }
