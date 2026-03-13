package core

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
)

// AppendVerification adds a validator confirmation to the block while enforcing
// ordering, uniqueness, and signature validity against the block hash.
func (b *Block) AppendVerification(v Verification, pubKey *ecdsa.PublicKey) error {
	if b == nil {
		return errors.New("block is nil")
	}
	if b.Finalized {
		return errors.New("block is finalized")
	}
	if pubKey == nil {
		return errors.New("verifier public key is required")
	}
	if v.VerifierID == "" {
		return errors.New("verifier id is required")
	}
	if len(v.Signature) == 0 {
		return errors.New("signature is required")
	}
	expectedIdx := 0
	if n := len(b.VerifiedSequence); n > 0 {
		expectedIdx = b.VerifiedSequence[n-1].Index + 1
	}
	if v.Index != expectedIdx {
		return fmt.Errorf("verification index mismatch: got %d want %d", v.Index, expectedIdx)
	}
	for _, existing := range b.VerifiedSequence {
		if existing.VerifierID == v.VerifierID {
			return fmt.Errorf("verifier %s already added to sequence", v.VerifierID)
		}
	}
	if b.Hash == "" {
		return errors.New("block hash is empty")
	}
	hashBytes, err := hex.DecodeString(b.Hash)
	if err != nil {
		return fmt.Errorf("invalid block hash: %w", err)
	}
	if !ecdsa.VerifyASN1(pubKey, hashBytes, v.Signature) {
		return errors.New("signature verification failed")
	}
	b.VerifiedSequence = append(b.VerifiedSequence, v)
	return nil
}
