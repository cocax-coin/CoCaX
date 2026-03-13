package core

import (
	"math"
)

// finalizationThreshold returns the number of validator confirmations required
// to finalize a block. It uses a 2/3 majority of active validators with a
// minimum static threshold to guard against very small validator sets.
func finalizationThreshold() int {
	validatorMu.RLock()
	active := 0
	for _, v := range ValidatorSet {
		if v.Active {
			active++
		}
	}
	validatorMu.RUnlock()

	dynamic := int(math.Ceil((2.0 * float64(active)) / 3.0))
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
	if len(block.Verifications) >= finalizationThreshold() {
		block.Finalized = true
	}
}
