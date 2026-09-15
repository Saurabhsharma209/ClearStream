package agentstream

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"go.uber.org/zap"
)

// erroringSuppressor is a model.Suppressor whose Process call always fails.
// It exercises handleMedia's pipeline-error branch: not reachable through
// any backend registered in pkg/model today (passthrough and rnnoise never
// fail Process in practice), but reachable in production if a suppressor
// backend (ONNX runtime crash, deepfilter-server RPC failure, etc.) errors
// mid-call.
type erroringSuppressor struct{}

func (erroringSuppressor) Process(frame []int16) ([]int16, error) {
	return nil, errSuppressorBoom
}
func (erroringSuppressor) Reset()       {}
func (erroringSuppressor) Close() error { return nil }
func (erroringSuppressor) Name() string { return "erroring-test-suppressor" }

var errSuppressorBoom = &suppressorBoomError{}

type suppressorBoomError struct{}

func (*suppressorBoomError) Error() string { return "boom: suppressor exploded" }

// TestHandleMediaPipelineProcessError locks in that handleMedia wraps and
// surfaces an error from Pipeline.ProcessFrames (e.g. a suppressor backend
// failure mid-call) instead of panicking or silently dropping the frame.
// Exercises server.go's previously-uncovered ProcessFrames-error branch
// inside handleMedia.
func TestHandleMediaPipelineProcessError(t *testing.T) {
	s := NewAgentStreamServer(ServerConfig{})

	pipeline := audio.NewPipeline(audio.PipelineConfig{
		Channels:        1,
		Suppressor:      erroringSuppressor{},
		Logger:          zap.NewNop(),
		InputSampleRate: 8000,
	})
	defer pipeline.Close()

	call := &callState{
		state:      StateStreaming,
		streamSID:  "ST-ERR",
		pipeline:   pipeline,
		sampleRate: 8000,
	}

	raw := make([]byte, 160)
	ev := &MediaEvent{
		Event:      EventMedia,
		StreamSID:  "ST-ERR",
		Track:      "inbound",
		Codec:      "audio/x-l16",
		SampleRate: 8000,
		Payload:    base64.StdEncoding.EncodeToString(raw),
	}

	err := s.handleMedia(nil, call, ev)
	if err == nil {
		t.Fatal("expected handleMedia to return an error when the pipeline fails to process a frame")
	}
	if !strings.Contains(err.Error(), "pipeline process") {
		t.Fatalf("expected error wrapped with pipeline process context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped error to retain underlying cause, got: %v", err)
	}
}
