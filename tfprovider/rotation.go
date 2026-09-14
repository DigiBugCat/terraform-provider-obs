package tfprovider

import "math"

// Scene-item rotation describes an orientation, not an animation's turn count.
// obs-websocket accepts [-360, 360] and libobs stores a float32. Keep valid
// signed angles; wrap excess whole turns at the transport boundary. Do not use
// this for Move filter settings, where additional turns can describe motion.
func rotationForOBS(degrees float64) float64 {
	if degrees < -360 || degrees > 360 {
		degrees = math.Mod(degrees, 360)
	}
	return float64(float32(degrees))
}

// Readback can contain either a bounded angle or legacy accumulated turns.
// Only exact native equivalence is accepted; real angular drift remains visible.
func rotationMatches(observed, authored float64) bool {
	if math.IsNaN(observed) || math.IsInf(observed, 0) || math.IsNaN(authored) || math.IsInf(authored, 0) {
		return false
	}
	expected := rotationForOBS(authored)
	return observed == expected || math.Remainder(observed-expected, 360) == 0
}
