package billing

// RateCard maps Feature combinations to INR price per billed unit (one pulse).
type RateCard interface {
	UnitPrice(features Feature) float64 // INR per billed unit (1 pulse = PulseMs)
}

// StaticRateCard is an in-memory RateCard for testing and simple deployments.
type StaticRateCard struct {
	BasePrice     float64             // base price (VAD only), INR
	FeaturePrices map[Feature]float64 // additional price per feature flag, INR
}

// UnitPrice returns the total price per billed unit for the given feature set.
// It sums BasePrice plus the per-feature surcharges for every active feature.
func (r *StaticRateCard) UnitPrice(features Feature) float64 {
	price := r.BasePrice
	for flag, extra := range r.FeaturePrices {
		if features.Has(flag) {
			price += extra
		}
	}
	return price
}

// DefaultTelephonyRateCard returns pricing for the cloud telephony use case.
// Base tier (VAD): ₹0.0000955 per 6-second pulse.
// Premium features add incremental cost per unit.
// INR figures are USD figures converted at ~95.5 INR/USD (Aug 2026); re-derive
// from the USD base (BasePrice $0.000001, surcharges as documented in git
// history) if the conversion rate needs to be refreshed.
func DefaultTelephonyRateCard() *StaticRateCard {
	return &StaticRateCard{
		BasePrice: 0.0000955, // ₹0.0000955/unit — VAD baseline
		FeaturePrices: map[Feature]float64{
			FeatureSpectralNR: 0.0000478, // +₹0.0000478/unit
			FeatureRNNoise:    0.0000955, // +₹0.0000955/unit
			FeatureDeepFilter: 0.0001433, // +₹0.0001433/unit (GPU tier)
			FeatureAGC:        0.0000191, // +₹0.0000191/unit
			FeatureRTPMonitor: 0.0000287, // +₹0.0000287/unit
			FeatureEval:       0.0000955, // +₹0.0000955/unit
		},
	}
}
