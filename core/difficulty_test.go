package core

import (
	"strings"
	"testing"
)

const maxNonceSearchAttempts = 1024

func TestCalculateNewDifficultyClampsAdjustmentBounds(t *testing.T) {
	first := &Block{Timestamp: 0}
	lastFast := &Block{Timestamp: TargetAdjustmentTimespanSeconds / 10, Difficulty: 8}
	lastSlow := &Block{Timestamp: TargetAdjustmentTimespanSeconds * 10, Difficulty: 8}

	fast := CalculateNewDifficulty(lastFast, first)
	if fast != 32 {
		t.Fatalf("expected 4x increase after fast window clamp, got %d", fast)
	}
	slow := CalculateNewDifficulty(lastSlow, first)
	if slow != 2 {
		t.Fatalf("expected 4x decrease after slow window clamp, got %d", slow)
	}
}

func TestExpectedDifficultyWindowBehavior(t *testing.T) {
	chain := make([]Block, 0, DifficultyAdjustmentWindow)
	for i := uint64(0); i < DifficultyAdjustmentWindow; i++ {
		chain = append(chain, Block{
			Index:      i,
			Timestamp:  int64(i) * TargetBlockIntervalSeconds,
			Difficulty: 4,
		})
	}
	got := ExpectedDifficulty(chain)
	if got != 4 {
		t.Fatalf("expected unchanged difficulty at target pace, got %d", got)
	}
}

func TestHashMeetsDifficulty(t *testing.T) {
	if !HashMeetsDifficulty("0000abcdef", 4) {
		t.Fatalf("expected hash to satisfy difficulty")
	}
	if HashMeetsDifficulty("00ffabcdef", 4) {
		t.Fatalf("expected hash to fail higher difficulty")
	}
}

func TestVerifyBlockRejectsDifficultyAndPoWMismatch(t *testing.T) {
	dir := t.TempDir()
	state, err := LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	block, err := CreateBlockTemplate(state, "miner-check")
	if err != nil {
		t.Fatalf("CreateBlockTemplate: %v", err)
	}

	badDifficulty := *block
	badDifficulty.Difficulty = block.Difficulty + 1
	if err := VerifyBlock(state, &badDifficulty); err == nil || !strings.Contains(err.Error(), "difficulty mismatch") {
		t.Fatalf("expected difficulty mismatch error, got: %v", err)
	}

	badPoW := *block
	for tries := 0; tries < maxNonceSearchAttempts; tries++ {
		badPoW.Nonce++
		badPoW.Hash = BlockHash(&badPoW)
		if !HashMeetsDifficulty(badPoW.Hash, badPoW.Difficulty) {
			break
		}
	}
	if HashMeetsDifficulty(badPoW.Hash, badPoW.Difficulty) {
		t.Fatalf("failed to construct invalid PoW test block")
	}
	if err := VerifyBlock(state, &badPoW); err == nil || !strings.Contains(err.Error(), "does not satisfy") {
		t.Fatalf("expected PoW validation error, got: %v", err)
	}
}
