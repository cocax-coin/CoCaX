package core

import (
	"testing"
	"time"
)

func TestResolveForkPrefersMoreConfirmations(t *testing.T) {
	dir := t.TempDir()
	state, _ := LoadState(dir, "")

	minerPriv, _ := GenerateKeyPair()
	minerAddr := DeriveAddress(&minerPriv.PublicKey)

	// Start with a canonical block #1.
	origBlock, err := MineBlock(state, minerAddr, dir)
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}

	// Build an alternative block at the same height with additional validator confirmations.
	genesis := state.Chain[0]
	alt := Block{
		Index:     origBlock.Index,
		PrevHash:  genesis.Hash,
		Timestamp: time.Now().Unix(),
		Difficulty: ExpectedDifficulty([]Block{
			genesis,
		}),
		Reward: BlockReward(origBlock.Index),
		Miner:  minerAddr,
		Transactions: []Transaction{
			{
				ID:             "coinbase-alt",
				From:           "coinbase",
				To:             minerAddr,
				Amount:         BlockReward(origBlock.Index),
				IsCoinbase:     true,
				SequenceNumber: 0,
				Timestamp:      time.Now().Unix(),
			},
		},
	}
	MineProofOfWork(&alt)

	ReplaceValidatorSet(map[string]Validator{
		"v1": {ID: "v1", Active: true},
		"v2": {ID: "v2", Active: true},
		"v3": {ID: "v3", Active: true},
	})
	defer ReplaceValidatorSet(map[string]Validator{})
	if err := AppendVerification(&alt, BlockVerification{Peer: "v1", Accepted: true}); err != nil {
		t.Fatalf("AppendVerification v1: %v", err)
	}
	if err := AppendVerification(&alt, BlockVerification{Peer: "v2", Accepted: true}); err != nil {
		t.Fatalf("AppendVerification v2: %v", err)
	}

	adopted, err := ResolveFork(state, &alt, dir)
	if err != nil {
		t.Fatalf("ResolveFork: %v", err)
	}
	if !adopted {
		t.Fatalf("expected alternate fork to be adopted")
	}

	state.RLock()
	defer state.RUnlock()
	if state.Chain[len(state.Chain)-1].Hash != alt.Hash {
		t.Fatalf("chain tip not updated to alternate fork")
	}
}
