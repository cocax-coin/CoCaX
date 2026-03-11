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

// ReplaceValidatorSet swaps the validator registry with the provided map in a
// thread-safe manner. Intended for tests or controlled initialization paths.
func ReplaceValidatorSet(set map[string]Validator) {
	validatorMu.Lock()
	defer validatorMu.Unlock()
	ValidatorSet = set
}

// SnapshotValidatorSet returns a copy of the current validator registry.
func SnapshotValidatorSet() map[string]Validator {
	validatorMu.RLock()
	defer validatorMu.RUnlock()
	out := make(map[string]Validator, len(ValidatorSet))
	for id, v := range ValidatorSet {
		out[id] = v
	}
	return out
}

// AppendVerification records a validator's verification vote on the given block.
// It first checks that the peer is a registered, active validator and returns an
// error otherwise. On success it appends the vote to the block's verification
// metadata. NOTE: callers must not invoke this concurrently on the same block
// instance.
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
