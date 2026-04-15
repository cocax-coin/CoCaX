package core

import (
	"strings"
)

const (
	// DifficultyAdjustmentWindow controls how often network difficulty is recalculated.
	DifficultyAdjustmentWindow = uint64(2016)
	// TargetBlockIntervalSeconds is the intended inter-block spacing.
	TargetBlockIntervalSeconds = int64(30)
	// TargetAdjustmentTimespanSeconds is the target duration of one adjustment window.
	TargetAdjustmentTimespanSeconds = int64(DifficultyAdjustmentWindow) * TargetBlockIntervalSeconds
	// InitialDifficulty is the default network difficulty for genesis / early chain.
	InitialDifficulty = uint32(1)
	// MaxPoWDifficulty bounds impossible PoW requirements for a 256-bit hex hash.
	MaxPoWDifficulty = uint32(64)
)

func clampDifficulty(diff uint32) uint32 {
	if diff == 0 {
		return InitialDifficulty
	}
	if diff > MaxPoWDifficulty {
		return MaxPoWDifficulty
	}
	return diff
}

// HashMeetsDifficulty validates that hash satisfies the required leading-zero rule.
func HashMeetsDifficulty(hash string, difficulty uint32) bool {
	difficulty = clampDifficulty(difficulty)
	if len(hash) < int(difficulty) {
		return false
	}
	return strings.HasPrefix(hash, strings.Repeat("0", int(difficulty)))
}

// CalculateNewDifficulty calculates windowed difficulty with dampening bounds.
func CalculateNewDifficulty(lastBlock, firstBlockInWindow *Block) uint32 {
	if lastBlock == nil || firstBlockInWindow == nil {
		return InitialDifficulty
	}
	actualTime := lastBlock.Timestamp - firstBlockInWindow.Timestamp
	targetTime := TargetAdjustmentTimespanSeconds

	if actualTime < targetTime/4 {
		actualTime = targetTime / 4
	}
	if actualTime > targetTime*4 {
		actualTime = targetTime * 4
	}
	if actualTime <= 0 {
		actualTime = 1
	}

	lastDifficulty := clampDifficulty(lastBlock.Difficulty)
	newDifficulty := uint32((uint64(lastDifficulty) * uint64(targetTime)) / uint64(actualTime))
	return clampDifficulty(newDifficulty)
}

// ExpectedDifficulty computes the required difficulty for the next block.
func ExpectedDifficulty(chain []Block) uint32 {
	if len(chain) == 0 {
		return InitialDifficulty
	}
	last := chain[len(chain)-1]
	nextIndex := last.Index + 1
	lastDifficulty := clampDifficulty(last.Difficulty)

	if nextIndex == 0 {
		return InitialDifficulty
	}
	if nextIndex%DifficultyAdjustmentWindow != 0 {
		return lastDifficulty
	}
	if uint64(len(chain)) < DifficultyAdjustmentWindow {
		return lastDifficulty
	}

	firstIdx := len(chain) - int(DifficultyAdjustmentWindow)
	first := chain[firstIdx]
	return CalculateNewDifficulty(&last, &first)
}

// MineProofOfWork updates nonce/hash until block satisfies its configured difficulty.
func MineProofOfWork(block *Block) {
	mineBlockPoW(block)
}
