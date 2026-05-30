package model

/*
#cgo LDFLAGS: -lrnnoise
#include <rnnoise.h>
#include <stdlib.h>

// rnnoise_process_frame expects float32 in [-32768, 32768] range.
// Returns voice activity probability [0,1].
static float process(DenoiseState *st, float *out, const float *in) {
    return rnnoise_process_frame(st, out, in);
}
*/
import "C"
import (
	"fmt"
	"sync"
	"unsafe"
)

const rnnoiseFrameSize = 480 // RNNoise requires exactly 480 samples @ 48kHz
// Our pipeline uses 16kHz/160 samples; we upsample before RNNoise and downsample after.
// For simplicity in R&D: use 48kHz internally when RNNoise is active.

// RNNoise wraps the libRNNoise C library.
// Install: https://github.com/xiph/rnnoise (build as shared library).
type RNNoise struct {
	mu    sync.Mutex
	state *C.DenoiseState
}

// NewRNNoise creates a new RNNoise suppressor.
// Requires libRNNoise to be installed on the system.
// Install on Ubuntu: apt-get install librnnoise-dev
// Install on macOS:  brew install rnnoise
func NewRNNoise() (*RNNoise, error) {
	st := C.rnnoise_create(nil)
	if st == nil {
		return nil, fmt.Errorf("rnnoise: failed to create DenoiseState")
	}
	return &RNNoise{state: st}, nil
}

// Process suppresses noise in a 160-sample 16kHz frame.
// Internally upsamples to 480 samples @ 48kHz for RNNoise, then downsamples.
func (r *RNNoise) Process(frame []int16) ([]int16, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Upsample 160 @ 16kHz → 480 @ 48kHz (3x linear)
	up := upsample3x(frame)

	// Convert int16 → float32 in RNNoise range
	fin := make([]C.float, rnnoiseFrameSize)
	fout := make([]C.float, rnnoiseFrameSize)
	for i, s := range up {
		fin[i] = C.float(s)
	}

	// Run RNNoise
	C.process(
		r.state,
		(*C.float)(unsafe.Pointer(&fout[0])),
		(*C.float)(unsafe.Pointer(&fin[0])),
	)

	// Convert back float32 → int16
	out48 := make([]int16, rnnoiseFrameSize)
	for i, f := range fout {
		v := int32(f)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out48[i] = int16(v)
	}

	// Downsample 480 @ 48kHz → 160 @ 16kHz (1/3)
	return downsample3x(out48), nil
}

// Reset clears the RNNoise internal state.
func (r *RNNoise) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != nil {
		C.rnnoise_destroy(r.state)
		r.state = C.rnnoise_create(nil)
	}
}

// Close frees the RNNoise state.
func (r *RNNoise) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != nil {
		C.rnnoise_destroy(r.state)
		r.state = nil
	}
	return nil
}

// Name returns the backend identifier.
func (r *RNNoise) Name() string { return "rnnoise" }

// ---- helpers ----------------------------------------------------------------

// upsample3x converts 16kHz (160 samples) to 48kHz (480 samples) by repetition.
// For better quality, use a proper anti-aliasing FIR filter.
func upsample3x(in []int16) []int16 {
	out := make([]int16, len(in)*3)
	for i, s := range in {
		out[i*3] = s
		out[i*3+1] = s
		out[i*3+2] = s
	}
	return out
}

// downsample3x converts 48kHz (480 samples) to 16kHz (160 samples) by decimation.
func downsample3x(in []int16) []int16 {
	out := make([]int16, len(in)/3)
	for i := range out {
		out[i] = in[i*3]
	}
	return out
}
