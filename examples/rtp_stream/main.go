// Example: live RTP noise suppression.
//
// Listens for RTP packets on :5004, suppresses noise, forwards to :5006.
// Simulates a transparent in-path media proxy.
//
// Run:
//   go run main.go --listen :5004 --forward localhost:5006
//
// Test with ffmpeg:
//   # Send a noisy RTP stream to :5004
//   ffmpeg -re -i noisy.wav -ar 8000 -ac 1 -f rtp rtp://localhost:5004
//
//   # Receive the cleaned stream on :5006
//   ffmpeg -i rtp://localhost:5006 -f wav clean_live.wav
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
	rtppkg "github.com/exotel/clearstream/pkg/rtp"
)

func main() {
	listen := flag.String("listen", ":5004", "UDP address to receive RTP")
	forward := flag.String("forward", "localhost:5006", "UDP address to forward clean RTP")
	codec := flag.String("codec", "auto", "pcmu | pcma | opus | g722 | g729 | auto")
	jitter := flag.Int("jitter", 4, "Jitter buffer depth in frames")
	model := flag.String("model", "rnnoise", "rnnoise | deepfilter | passthrough")
	flag.Parse()

	cs, err := clearstream.New(clearstream.Config{Model: *model})
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	defer cs.Close()

	rtpCfg := rtppkg.Config{
		ListenAddr:  *listen,
		ForwardAddr: *forward,
		JitterDepth: *jitter,
		OnStats: func(s rtppkg.Stats) {
			fmt.Printf("\r[rtp] rx=%-6d tx=%-6d lost=%-4d latency=%.1fms",
				s.PacketsReceived, s.PacketsSent, s.PacketsLost, s.LatencyAvgMs)
		},
	}
	if *codec != "auto" {
		rtpCfg.Codec = audio.Codec(*codec)
	}

	session, err := cs.NewRTPSession(rtpCfg)
	if err != nil {
		log.Fatalf("create session: %v", err)
	}

	session.Start()
	fmt.Printf("ClearStream RTP running  %s → [AI suppress] → %s\n", *listen, *forward)
	fmt.Println("Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nStopping...")
	session.Stop()
}
