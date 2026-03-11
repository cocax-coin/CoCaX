package core

import "testing"

func TestAppendVerificationRejectsUnknownValidator(t *testing.T) {
	orig := ValidatorSet
	ValidatorSet = make(map[string]Validator)
	defer func() { ValidatorSet = orig }()

	block := &Block{}
	err := block.AppendVerification(BlockVerification{Peer: "validator-1", Accepted: true})
	if err == nil {
		t.Fatalf("expected error for unknown validator")
	}
}

func TestAppendVerificationRejectsInactiveValidator(t *testing.T) {
	orig := ValidatorSet
	ValidatorSet = map[string]Validator{
		"validator-1": {ID: "validator-1", Active: false},
	}
	defer func() { ValidatorSet = orig }()

	block := &Block{}
	err := block.AppendVerification(BlockVerification{Peer: "validator-1", Accepted: true})
	if err == nil {
		t.Fatalf("expected error for inactive validator")
	}
}

func TestAppendVerificationSucceedsForActiveValidator(t *testing.T) {
	orig := ValidatorSet
	ValidatorSet = map[string]Validator{
		"validator-1": {ID: "validator-1", Active: true},
	}
	defer func() { ValidatorSet = orig }()

	block := &Block{}
	vote := BlockVerification{Peer: "validator-1", Accepted: true}
	if err := block.AppendVerification(vote); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(block.Verifications) != 1 {
		t.Fatalf("expected 1 verification, got %d", len(block.Verifications))
	}
	if !block.BlockVerifications["validator-1"] {
		t.Fatalf("block verification map not updated")
	}
}
