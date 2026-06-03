package model

// NoiseProfile defines suppressor and VAD tuning for a specific noise environment.
// Different environments need different aggressiveness — what works for an office
// is too aggressive for a PSTN call and too mild for a call center floor.
type NoiseProfile struct {
	Name string

	// SuppressorConfig is the base suppressor configuration.
	SuppressorConfig SuppressorConfig

	// VADThreshold is the speech probability threshold (0–1).
	// Lower = more sensitive (catches faint speech, risks noise bleed-through).
	// Higher = more conservative (misses quiet speech, cleaner in loud environments).
	VADThreshold float64

	// AGCTargetRMS is the target loudness after gain control (0–1 normalized).
	// Indian PSTN calls tend to be quieter; slightly higher target helps.
	AGCTargetRMS float64

	// Description explains the intended use case.
	Description string

	// Narrowband indicates this profile is tuned for 8kHz (Indian PSTN).
	Narrowband bool
}

// IndiaCallCenterProfile returns a NoiseProfile for Indian call centers on PSTN.
// Tuned for: G.711 µ-law 8kHz, keyboard noise, HVAC hum, background babble,
// headset coloration, Indian English phonetics (retroflex consonants, dental stops).
//
// Key decisions:
//   - Lower VAD threshold (0.25) to catch retroflex consonants which have lower
//     energy concentration than English stops
//   - Higher AGC target (0.35) because PSTN calls arrive quieter than VoIP
//   - Passthrough suppressor by default (real RNNoise needs build tag)
func IndiaCallCenterProfile() NoiseProfile {
	return NoiseProfile{
		Name: "india-call-center",
		SuppressorConfig: SuppressorConfig{
			Backend:        "passthrough", // upgrade to "rnnoise" with -tags rnnoise
			Aggressiveness: 2,             // medium suppression (1=mild, 3=aggressive)
		},
		VADThreshold: 0.25, // tuned for Indian English phonetics on PSTN
		AGCTargetRMS: 0.35, // louder target: PSTN calls arrive quiet
		Narrowband:   true,
		Description:  "Indian call center: G.711 PSTN, keyboard/HVAC/babble noise, Indian English phonetics",
	}
}

// IndiaWidebandProfile returns a NoiseProfile for Indian VoIP/SIP wideband trunks.
// Tuned for: G.722 16kHz, SIP trunk noise, wideband headsets.
func IndiaWidebandProfile() NoiseProfile {
	return NoiseProfile{
		Name: "india-wideband",
		SuppressorConfig: SuppressorConfig{
			Backend:        "passthrough",
			Aggressiveness: 1, // milder: wideband already has better SNR
		},
		VADThreshold: 0.20, // wideband captures more speech detail, lower threshold
		AGCTargetRMS: 0.30,
		Narrowband:   false,
		Description:  "Indian VoIP/SIP: G.722 16kHz, wideband trunk noise, Indian English",
	}
}

// GenericOfficeProfile is a baseline profile for standard office environments.
func GenericOfficeProfile() NoiseProfile {
	return NoiseProfile{
		Name:             "office",
		SuppressorConfig: SuppressorConfig{Backend: "passthrough", Aggressiveness: 1},
		VADThreshold:     0.30,
		AGCTargetRMS:     0.25,
		Narrowband:       false,
		Description:      "Generic office: mild HVAC, keyboard, occasional speech",
	}
}
