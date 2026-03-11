package core

import "testing"

func TestAppendVerificationRejectsUnknownValidator(t *testing.T) {
	orig := SnapshotValidatorSet()
	ReplaceValidatorSet(make(map[string]Validator))
	defer ReplaceValidatorSet(orig)

	block := &Block{}
	err := AppendVerification(block, BlockVerification{Peer: "validator-1", Accepted: true})
	if err == nil {
		t.Fatalf("expected error for unknown validator")
	}
}

func TestAppendVerificationRejectsInactiveValidator(t *testing.T) {
	orig := SnapshotValidatorSet()
	ReplaceValidatorSet(map[string]Validator{
		"validator-1": {ID: "validator-1", Active: false},
	})
	defer ReplaceValidatorSet(orig)

	block := &Block{}
	err := AppendVerification(block, BlockVerification{Peer: "validator-1", Accepted: true})
	if err == nil {
		t.Fatalf("expected error for inactive validator")
	}
}

func TestAppendVerificationSucceedsForActiveValidator(t *testing.T) {
	orig := SnapshotValidatorSet()
	ReplaceValidatorSet(map[string]Validator{
		"validator-1": {ID: "validator-1", Active: true},
	})
	defer ReplaceValidatorSet(orig)

	block := &Block{}
	vote := BlockVerification{Peer: "validator-1", Accepted: true}
	if err := AppendVerification(block, vote); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(block.Verifications) != 1 {
		t.Fatalf("expected 1 verification, got %d", len(block.Verifications))
	}
	if isAccepted, ok := block.BlockVerifications["validator-1"]; !ok || !isAccepted {
		t.Fatalf("block verification map not updated")
	}
}
