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

func TestBlockWorkScaling(t *testing.T) {
	tests := []struct {
		difficulty uint32
		want       string
	}{
		{0, "16"},
		{1, "16"},
		{2, "256"},
		{3, "4096"},
		{MaxPoWDifficulty, BlockWork(MaxPoWDifficulty).String()},
	}
	for _, tt := range tests {
		got := BlockWork(tt.difficulty).String()
		if got != tt.want {
			t.Fatalf("BlockWork(%d): got %s want %s", tt.difficulty, got, tt.want)
		}
	}
}

func TestTotalDifficulty(t *testing.T) {
	if got := TotalDifficulty(nil).String(); got != "0" {
		t.Fatalf("empty chain total difficulty mismatch: %s", got)
	}
	chain := []Block{
		{Difficulty: 1},
		{Difficulty: 2},
		{Difficulty: 3},
	}
	// 16 + 256 + 4096 = 4368
	if got := TotalDifficulty(chain).String(); got != "4368" {
		t.Fatalf("total difficulty mismatch: got %s want 4368", got)
	}
}
