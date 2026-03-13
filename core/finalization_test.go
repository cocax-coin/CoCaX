package core

import (
	"fmt"
	"testing"
)

func TestBlockFinalizesAfterThresholdReached(t *testing.T) {
	orig := SnapshotValidatorSet()
	ReplaceValidatorSet(map[string]Validator{
		"v1": {ID: "v1", Active: true},
		"v2": {ID: "v2", Active: true},
		"v3": {ID: "v3", Active: true},
	})
	defer ReplaceValidatorSet(orig)

	blk := &Block{}
	votes := []BlockVerification{
		{Peer: "v1", Accepted: true},
		{Peer: "v2", Accepted: true},
		{Peer: "v3", Accepted: true},
	}
	for i, vote := range votes {
		if err := AppendVerification(blk, vote); err != nil {
			t.Fatalf("append verification %d: %v", i, err)
		}
		if i < FinalizationThreshold-1 && blk.Finalized {
			t.Fatalf("block finalized too early after %d votes", i+1)
		}
	}
	if !blk.Finalized {
		t.Fatalf("block should be finalized after %d votes", len(votes))
	}
}

func TestAppendVerificationRejectsAfterFinalization(t *testing.T) {
	orig := SnapshotValidatorSet()
	ReplaceValidatorSet(map[string]Validator{
		"v1": {ID: "v1", Active: true},
		"v2": {ID: "v2", Active: true},
		"v3": {ID: "v3", Active: true},
		"v4": {ID: "v4", Active: true},
	})
	defer ReplaceValidatorSet(orig)

	blk := &Block{}
	initial := []BlockVerification{
		{Peer: "v1", Accepted: true},
		{Peer: "v2", Accepted: true},
		{Peer: "v3", Accepted: true},
	}
	for _, vote := range initial {
		if err := AppendVerification(blk, vote); err != nil {
			t.Fatalf("setup vote failed: %v", err)
		}
	}
	if !blk.Finalized {
		t.Fatal("block expected to be finalized after threshold votes")
	}
	if err := AppendVerification(blk, BlockVerification{Peer: "v4", Accepted: true}); err == nil {
		t.Fatalf("expected rejection for finalized block")
	}
	if len(blk.Verifications) != len(initial) {
		t.Fatalf("verifications mutated after rejection: got %d want %d", len(blk.Verifications), len(initial))
	}
}

func TestResolveForkPrefersFinalizedBlocks(t *testing.T) {
	dir := t.TempDir()
	state, _ := LoadState(dir, "")

	minerPriv, _ := GenerateKeyPair()
	minerAddr := DeriveAddress(&minerPriv.PublicKey)

	blockA, err := MineBlock(state, minerAddr, dir)
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}

	altBlock := *blockA
	altBlock.Miner = fmt.Sprintf("%s-alt", minerAddr)
	altBlock.Hash = BlockHash(&altBlock)
	altBlock.Finalized = true
	altBlock.Verifications = make([]BlockVerification, FinalizationThreshold)
	for i := 0; i < FinalizationThreshold; i++ {
		altBlock.Verifications[i] = BlockVerification{Peer: fmt.Sprintf("peer-%d", i), Accepted: true}
	}

	adopted, err := ResolveFork(state, &altBlock, dir)
	if err != nil {
		t.Fatalf("ResolveFork returned error: %v", err)
	}
	if !adopted {
		t.Fatalf("expected finalized alternate block to be adopted")
	}

	state.RLock()
	defer state.RUnlock()
	if len(state.Chain) == 0 {
		t.Fatalf("state chain empty after fork resolution")
	}
	tip := state.Chain[len(state.Chain)-1]
	if tip.Hash != altBlock.Hash {
		t.Fatalf("expected alternate block at tip, got %s", tip.Hash)
	}
	if !tip.Finalized {
		t.Fatalf("adopted block should be finalized")
	}
}
