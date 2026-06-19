package rtp

import (
	"bytes"
	"testing"
)

func TestPlaybackQueue_PushPop(t *testing.T) {
	q := NewPlaybackQueue(10)
	frame := []byte{0x01, 0x02, 0x03}
	if !q.Push(frame) {
		t.Fatal("Push returned false on non-full queue")
	}
	got := q.Pop()
	if got == nil {
		t.Fatal("Pop returned nil, expected a frame")
	}
	if !bytes.Equal(got, frame) {
		t.Errorf("Pop returned %v, expected %v", got, frame)
	}
}

func TestPlaybackQueue_Empty(t *testing.T) {
	q := NewPlaybackQueue(10)
	got := q.Pop()
	if got != nil {
		t.Errorf("Pop on empty queue should return nil, got %v", got)
	}
}

func TestPlaybackQueue_Full(t *testing.T) {
	q := NewPlaybackQueue(3)
	frame := []byte{0xAA}
	q.Push(frame)
	q.Push(frame)
	q.Push(frame)
	// Queue is now full; next Push should fail
	ok := q.Push(frame)
	if ok {
		t.Error("Push on full queue should return false")
	}
	stats := q.Stats()
	if stats.Dropped != 1 {
		t.Errorf("expected Dropped=1, got %d", stats.Dropped)
	}
}

func TestPlaybackQueue_Clear(t *testing.T) {
	q := NewPlaybackQueue(10)
	frame := []byte{0x01}
	q.Push(frame)
	q.Push(frame)
	q.Push(frame)
	n := q.Clear()
	if n != 3 {
		t.Errorf("Clear returned %d, expected 3", n)
	}
	if q.Len() != 0 {
		t.Errorf("Len after Clear should be 0, got %d", q.Len())
	}
}

func TestPlaybackQueue_Len(t *testing.T) {
	q := NewPlaybackQueue(10)
	if q.Len() != 0 {
		t.Errorf("initial Len should be 0, got %d", q.Len())
	}
	q.Push([]byte{1})
	if q.Len() != 1 {
		t.Errorf("Len after one Push should be 1, got %d", q.Len())
	}
	q.Push([]byte{2})
	if q.Len() != 2 {
		t.Errorf("Len after two Pushes should be 2, got %d", q.Len())
	}
	q.Pop()
	if q.Len() != 1 {
		t.Errorf("Len after Pop should be 1, got %d", q.Len())
	}
}

func TestPlaybackQueue_Stats(t *testing.T) {
	q := NewPlaybackQueue(2)
	frame := []byte{0xFF}
	q.Push(frame) // pushed=1
	q.Push(frame) // pushed=2
	q.Push(frame) // dropped=1 (full)
	q.Pop()       // popped=1
	q.Clear()     // cleared=1

	stats := q.Stats()
	if stats.Pushed != 2 {
		t.Errorf("expected Pushed=2, got %d", stats.Pushed)
	}
	if stats.Popped != 1 {
		t.Errorf("expected Popped=1, got %d", stats.Popped)
	}
	if stats.Dropped != 1 {
		t.Errorf("expected Dropped=1, got %d", stats.Dropped)
	}
	if stats.Cleared != 1 {
		t.Errorf("expected Cleared=1, got %d", stats.Cleared)
	}
}

func TestPlaybackQueue_DefaultDepth(t *testing.T) {
	q := NewPlaybackQueue(0)
	if q.maxDepth != 50 {
		t.Errorf("expected maxDepth=50, got %d", q.maxDepth)
	}
}

func TestPlaybackQueue_PushCopiesFrame(t *testing.T) {
	q := NewPlaybackQueue(10)
	frame := []byte{0x01, 0x02, 0x03}
	q.Push(frame)
	// Mutate original
	frame[0] = 0xFF
	got := q.Pop()
	if got[0] == 0xFF {
		t.Error("Push did not copy the frame; mutation of original affected queued data")
	}
}
