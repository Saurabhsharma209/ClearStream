package rtp

import (
	"testing"
)

func TestParseDTMFPayload_Digit0(t *testing.T) {
	d := NewDTMFDetector(8000)
	payload := []byte{0, 0x80, 0x00, 0x50}
	digit, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if digit == nil {
		t.Fatal("expected a DTMFDigit, got nil")
	}
	if digit.Digit != "0" {
		t.Errorf("expected digit '0', got %q", digit.Digit)
	}
	if !digit.End {
		t.Error("expected End=true")
	}
	if digit.Volume != 0 {
		t.Errorf("expected Volume=0, got %d", digit.Volume)
	}
}

func TestParseDTMFPayload_DigitStar(t *testing.T) {
	d := NewDTMFDetector(8000)
	payload := []byte{10, 0x00, 0x00, 0x50}
	digit, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if digit == nil {
		t.Fatal("expected a DTMFDigit, got nil")
	}
	if digit.Digit != "*" {
		t.Errorf("expected digit '*', got %q", digit.Digit)
	}
	if digit.End {
		t.Error("expected End=false")
	}
}

func TestParseDTMFPayload_Pound(t *testing.T) {
	d := NewDTMFDetector(8000)
	payload := []byte{11, 0x80, 0x00, 0x50}
	digit, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if digit == nil {
		t.Fatal("expected a DTMFDigit, got nil")
	}
	if digit.Digit != "#" {
		t.Errorf("expected digit '#', got %q", digit.Digit)
	}
	if !digit.End {
		t.Error("expected End=true")
	}
}

func TestParseDTMFPayload_TooShort(t *testing.T) {
	d := NewDTMFDetector(8000)
	_, err := d.ParseDTMFPayload([]byte{0, 0x80, 0x00})
	if err == nil {
		t.Error("expected error for too-short payload, got nil")
	}
}

func TestParseDTMFPayload_UnknownEvent(t *testing.T) {
	d := NewDTMFDetector(8000)
	payload := []byte{20, 0x00, 0x00, 0x50}
	digit, err := d.ParseDTMFPayload(payload)
	if err == nil {
		t.Error("expected error for unknown event code, got nil")
	}
	if digit != nil {
		t.Error("expected nil digit for unknown event code")
	}
}

func TestParseDTMFPayload_Duplicate(t *testing.T) {
	d := NewDTMFDetector(8000)
	payload := []byte{0, 0x80, 0x00, 0x50}
	first, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if first == nil {
		t.Fatal("first call: expected a DTMFDigit, got nil")
	}
	second, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if second != nil {
		t.Error("second call: expected nil for duplicate, got a DTMFDigit")
	}
}

func TestParseDTMFPayload_DurationMs(t *testing.T) {
	d := NewDTMFDetector(8000)
	// duration=800 ticks at 8000Hz => 800*1000/8000 = 100ms; 0x0320 = 800
	payload := []byte{1, 0x00, 0x03, 0x20}
	digit, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if digit == nil {
		t.Fatal("expected a DTMFDigit, got nil")
	}
	if digit.DurationMs != 100 {
		t.Errorf("expected DurationMs=100, got %d", digit.DurationMs)
	}
}

func TestDTMFDetectorReset(t *testing.T) {
	d := NewDTMFDetector(8000)
	payload := []byte{0, 0x80, 0x00, 0x50}
	if _, err := d.ParseDTMFPayload(payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, _ := d.ParseDTMFPayload(payload)
	if second != nil {
		t.Fatal("expected nil for duplicate before reset")
	}
	d.Reset()
	third, err := d.ParseDTMFPayload(payload)
	if err != nil {
		t.Fatalf("post-reset: unexpected error: %v", err)
	}
	if third == nil {
		t.Error("post-reset: expected a DTMFDigit, got nil")
	}
}

func TestNewDTMFDetector_ZeroSampleRate(t *testing.T) {
	d := NewDTMFDetector(0)
	if d.sampleRate != 8000 {
		t.Errorf("expected default sampleRate=8000, got %d", d.sampleRate)
	}
}
