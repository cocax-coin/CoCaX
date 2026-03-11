package core

import (
	"fmt"
	"sync"
)

// Validator represents a registered PoVS validator.
type Validator struct {
	ID        string
	PublicKey []byte
	Active    bool
}

// ValidatorSet holds the registered validators keyed by their ID.
var ValidatorSet = make(map[string]Validator)
var validatorMu sync.RWMutex

// AppendVerification records a validator's verification vote on the given block.
// It rejects votes from unknown or inactive validators to prevent Sybil attacks.
func (b *Block) AppendVerification(v BlockVerification) error {
	if b == nil {
		return fmt.Errorf("block is nil")
	}
	validatorMu.RLock()
	validator, ok := ValidatorSet[v.Peer]
	validatorMu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown validator: %s", v.Peer)
	}
	if !validator.Active {
		return fmt.Errorf("inactive validator: %s", v.Peer)
	}

	if b.BlockVerifications == nil {
		b.BlockVerifications = make(map[string]bool)
	}
	b.Verifications = append(b.Verifications, v)
	b.BlockVerifications[v.Peer] = v.Accepted
	return nil
}

// AppendVerification is a helper that records a verification vote on the block.
// It delegates to the Block method to keep call-sites simple.
func AppendVerification(block *Block, v BlockVerification) error {
	return block.AppendVerification(v)
}
