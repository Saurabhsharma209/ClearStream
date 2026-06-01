// Load test: simulates N concurrent RTP sessions through ClearStream.
// Usage: go run tools/load_test/main.go --sessions 50 --duration 10s
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
)

func main() {
	sessions := flag.Int("sessions", 50, "concurrent sessions")
	duration := flag.Duration("duration", 10*time.Second, "test duration")
	flag.Parse()

	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		panic(err)
	}
	defer cs.Close()

	var (
		totalFrames int64
		totalErrors int64
		wg          sync.WaitGroup
	)

	start := time.Now()
	deadline := start.Add(*duration)

	fmt.Printf("Starting load test: %d sessions for %s\n", *sessions, *duration)

	// 10ms frame at 16kHz = 160 samples = 320 bytes (little-endian int16 PCM).
	frameBytes := make([]byte, audio.FrameSizeBytes)
	for j := 0; j < audio.FrameSizeSamples; j++ {
		v := int16(math.Sin(float64(j)*0.1) * 16000)
		frameBytes[j*2] = byte(v)
		frameBytes[j*2+1] = byte(v >> 8)
	}

	for i := 0; i < *sessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pipe := cs.Pipeline()
			var outBuf bytes.Buffer
			for time.Now().Before(deadline) {
				outBuf.Reset()
				if err := pipe.ProcessFrames(frameBytes, &outBuf); err != nil {
					atomic.AddInt64(&totalErrors, 1)
				} else {
					atomic.AddInt64(&totalFrames, 1)
				}
				// Real-time pacing: one 10ms frame per 10ms wall-clock.
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)
	fps := float64(totalFrames) / elapsed.Seconds()

	fmt.Printf("\n=== Load Test Results ===\n")
	fmt.Printf("Sessions:     %d\n", *sessions)
	fmt.Printf("Duration:     %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Total frames: %d\n", totalFrames)
	fmt.Printf("Errors:       %d\n", totalErrors)
	// 100 fps per session = real-time (10ms frames, 100 per second).
	fmt.Printf("Throughput:   %.0f frames/sec (%.1f sessions capacity)\n", fps, fps/100)
	fmt.Printf("Per-session:  %.1f fps (%.0f%% of real-time 100fps)\n",
		fps/float64(*sessions), fps/float64(*sessions))
}
