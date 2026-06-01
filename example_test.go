package clearstream_test

import (
	"fmt"
	"os"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/rtp"
)

// ExampleNew demonstrates creating a ClearStream instance with default config.
func ExampleNew() {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer cs.Close()
	fmt.Println("ClearStream ready, version:", clearstream.Version)
	// Output:
	// ClearStream ready, version: 0.1.0
}

// ExampleClearStream_NewRTPSession demonstrates configuring a live RTP session.
// The session is created but not started so this example has no side-effects.
func ExampleClearStream_NewRTPSession() {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer cs.Close()

	sess, err := cs.NewRTPSession(rtp.Config{
		ListenAddr:  "127.0.0.1:15004",
		ForwardAddr: "127.0.0.1:15005",
		PayloadType: 0, // PCMU / G.711 µ-law
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	// sess.Start() would begin live processing; Stop() to shut down.
	_ = sess
	fmt.Println("RTP session configured")
	// Output:
	// RTP session configured
}
