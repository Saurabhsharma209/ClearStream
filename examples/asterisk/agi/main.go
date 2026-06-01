// Package main implements an Asterisk EAGI handler for ClearStream.
//
// EAGI (Extended AGI) passes audio on file descriptor 3 in addition to the
// usual AGI stdin/stdout control channel.  This binary reads raw 8 kHz 16-bit
// mono PCM from fd 3, suppresses noise via ClearStream, and writes clean PCM
// back to stdout so Asterisk can play it to the far end.
//
// Add to your Asterisk dialplan (extensions.conf):
//
//	exten => s,1,EAGI(/usr/local/bin/clearstream-agi)
//
// Build:
//
//	go build -o /usr/local/bin/clearstream-agi ./examples/asterisk/agi_handler.go
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
)

func main() {
	// AGI handshake: read agi_* variables until blank line.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		fmt.Fprintln(os.Stderr, "[clearstream-agi] "+line)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "[clearstream-agi] stdin read error:", err)
		os.Exit(1)
	}

	// ClearStream init — 8 kHz to match Asterisk SLIN codec.
	cfg := clearstream.DefaultConfig()
	cfg.SampleRate = 8000

	cs, err := clearstream.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[clearstream-agi] clearstream init:", err)
		os.Exit(1)
	}
	defer cs.Close()

	pipe := cs.Pipeline()

	// EAGI audio channel: Asterisk opens raw PCM on fd 3.
	audioIn := os.NewFile(3, "audio-in")
	if audioIn == nil {
		fmt.Fprintln(os.Stderr, "[clearstream-agi] fd 3 not available — use EAGI() not AGI()")
		os.Exit(1)
	}

	stdoutW := bufio.NewWriterSize(os.Stdout, audio.FrameSizeBytes*16)

	// Read 10 ms frames (160 bytes at 8 kHz 16-bit mono), suppress, write back.
	buf := make([]byte, audio.FrameSizeBytes)
	for {
		if _, err := io.ReadFull(audioIn, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			fmt.Fprintln(os.Stderr, "[clearstream-agi] audio read:", err)
			break
		}

		var out bytes.Buffer
		if err := pipe.ProcessFrames(buf, &out); err != nil {
			fmt.Fprintln(os.Stderr, "[clearstream-agi] process:", err)
			break
		}

		if out.Len() > 0 {
			if err := binary.Write(stdoutW, binary.LittleEndian, out.Bytes()); err != nil {
				fmt.Fprintln(os.Stderr, "[clearstream-agi] write:", err)
				break
			}
		}
	}

	// Flush partial trailing frame.
	var tail bytes.Buffer
	_ = pipe.Flush(&tail)
	if tail.Len() > 0 {
		_, _ = stdoutW.Write(tail.Bytes())
	}
	_ = stdoutW.Flush()

	// Signal Asterisk that we are done.
	fmt.Fprintln(os.Stdout, "HANGUP")
}
