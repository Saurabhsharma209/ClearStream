package billing

// RateCard maps Feature combinations to USD price per billed unit (one pulse).
type RateCard interface {
	UnitPrice(features Feature) float64 // USD per billed unit (1 pulse = PulseMs)
}

// StaticRateCard is an in-memory RateCard for testing and simple deployments.
type StaticRateCard struct {
	BasePrice     float64             // base price (VAD only)
	FeaturePrices map[Feature]float64 // additional price per feature flag
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

// DefaultTelephonyRateCard returns pricing for the Exotel telephony use case.
// Base tier (VAD): $0.000001 per 6-second pulse.
// Premium features add incremental cost per unit.
func DefaultTelephonyRateCard() *StaticRateCard {
	return &StaticRateCard{
		BasePrice: 0.000001, // $0.000001/unit — VAD baseline
		FeaturePrices: map[Feature]float64{
			FeatureSpectralNR: 0.0000005, // +$0.0000005/unit
			FeatureRNNoise:    0.000001,  // +$0.000001/unit
			FeatureDeepFilter: 0.0000015, // +$0.0000015/unit (GPU tier)
			FeatureAGC:        0.0000002, // +$0.0000002/unit
			FeatureRTPMonitor: 0.0000003, // +$0.0000003/unit
			FeatureEval:       0.000001,  // +$0.000001/unit
		},
	}
}
