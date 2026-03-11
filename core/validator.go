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
// NOTE: callers must not invoke this concurrently on the same block instance.
func AppendVerification(block *Block, v BlockVerification) error {
	if block == nil {
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

	if block.BlockVerifications == nil {
		block.BlockVerifications = make(map[string]bool)
	}
	block.Verifications = append(block.Verifications, v)
	block.BlockVerifications[v.Peer] = v.Accepted
	return nil
}

// AppendVerification appends a verification vote using method syntax.
func (b *Block) AppendVerification(v BlockVerification) error {
	return AppendVerification(b, v)
}
