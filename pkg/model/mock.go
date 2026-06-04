package model

// MockSuppressor is a test double for Suppressor.
// It applies a fixed gain multiplier to each sample and tracks call counts.
type MockSuppressor struct {
	Gain         float64 // multiplier applied to each sample (default 1.0 = passthrough)
	ProcessCalls int
	ResetCalls   int
}

// NewMockSuppressor returns a MockSuppressor with passthrough gain.
func NewMockSuppressor() *MockSuppressor { return &MockSuppressor{Gain: 1.0} }

func (m *MockSuppressor) Process(frame []int16) ([]int16, error) {
	m.ProcessCalls++
	out := make([]int16, len(frame))
	for i, s := range frame {
		v := float64(s) * m.Gain
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out, nil
}
func (m *MockSuppressor) Reset()       { m.ResetCalls++ }
func (m *MockSuppressor) Close() error { return nil }
func (m *MockSuppressor) Name() string { return "mock" }

// ProcessBatch implements BatchSuppressor.
func (m *MockSuppressor) ProcessBatch(frames [][]int16) ([][]int16, error) {
	out := make([][]int16, len(frames))
	for i, f := range frames {
		processed, err := m.Process(f)
		if err != nil {
			return out, err
		}
		out[i] = processed
	}
	return out, nil
}
