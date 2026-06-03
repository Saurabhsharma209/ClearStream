package model

import (
	"testing"
)

func TestIndiaCallCenterProfile(t *testing.T) {
	p := IndiaCallCenterProfile()
	if p.Name != "india-call-center" {
		t.Errorf("Name: got %q, want %q", p.Name, "india-call-center")
	}
	if !p.Narrowband {
		t.Error("Narrowband: expected true")
	}
	if p.VADThreshold != 0.25 {
		t.Errorf("VADThreshold: got %v, want 0.25", p.VADThreshold)
	}
	if p.AGCTargetRMS != 0.35 {
		t.Errorf("AGCTargetRMS: got %v, want 0.35", p.AGCTargetRMS)
	}
	if p.SuppressorConfig.Aggressiveness != 2 {
		t.Errorf("Aggressiveness: got %d, want 2", p.SuppressorConfig.Aggressiveness)
	}
}

func TestIndiaWidebandProfile(t *testing.T) {
	p := IndiaWidebandProfile()
	if p.Name != "india-wideband" {
		t.Errorf("Name: got %q, want %q", p.Name, "india-wideband")
	}
	if p.Narrowband {
		t.Error("Narrowband: expected false")
	}
	if p.VADThreshold != 0.20 {
		t.Errorf("VADThreshold: got %v, want 0.20", p.VADThreshold)
	}
}

func TestGenericOfficeProfile(t *testing.T) {
	p := GenericOfficeProfile()
	if p.Name != "office" {
		t.Errorf("Name: got %q, want %q", p.Name, "office")
	}
}
