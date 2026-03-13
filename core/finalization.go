package core

import (
	"math"
)

// calculateFinalizationThreshold returns the number of validator confirmations required
// to finalize a block. It uses a ceil(2/3) fraction of active validators with a
// minimum static threshold to guard against very small validator sets.
func calculateFinalizationThreshold() int {
	validatorMu.RLock()
	active := 0
	for _, v := range ValidatorSet {
		if v.Active {
			active++
		}
	}
	validatorMu.RUnlock()

	if active == 0 {
		return -1
	}
	dynamic := int(math.Ceil((float64(active) * 2.0) / 3.0))
	if dynamic < 1 {
		dynamic = 1
	}
	if FinalizationThreshold > dynamic {
		return FinalizationThreshold
	}
	return dynamic
}

// markFinalizedIfThresholdReached toggles Finalized when enough verifications
// have been collected.
func markFinalizedIfThresholdReached(block *Block) {
	if block == nil || block.Finalized {
		return
	}
	threshold := calculateFinalizationThreshold()
	if threshold <= 0 {
		return
	}
	accepted := 0
	for _, v := range block.Verifications {
		if v.Accepted {
			accepted++
		}
	}
	if accepted >= threshold {
		block.Finalized = true
	}
}
