package core

import (
	"fmt"
	"sort"
	"time"
)

const maxTxFutureSkew = 5 * time.Minute

func pruneExpiredMempoolLocked(state *ChainState, now time.Time) {
	if state == nil || len(state.Mempool) == 0 {
		return
	}
	cutoff := now.Add(-MempoolTxTTL).Unix()
	kept := state.Mempool[:0]
	for _, tx := range state.Mempool {
		if tx.Timestamp >= cutoff {
			kept = append(kept, tx)
		}
	}
	state.Mempool = kept
}

// PruneExpiredMempool removes pending transactions older than MempoolTxTTL.
func PruneExpiredMempool(state *ChainState) {
	if state == nil {
		return
	}
	state.Lock()
	defer state.Unlock()
	pruneExpiredMempoolLocked(state, time.Now())
}

// AddTxToMempool validates a transaction against the current chain state and,
// on success, inserts it into the mempool with deterministic ordering.
func AddTxToMempool(state *ChainState, tx *Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}
	if tx.IsCoinbase {
		return fmt.Errorf("cannot submit coinbase transaction")
	}
	if !FloatEqual(tx.Fee, FixedFee) {
		return fmt.Errorf("fee must be exactly %.2f CoX", FixedFee)
	}
	if err := VerifyTransaction(tx); err != nil {
		return fmt.Errorf("signature validation failed: %w", err)
	}

	state.Lock()
	defer state.Unlock()
	now := time.Now()
	pruneExpiredMempoolLocked(state, now)
	if tx.Timestamp < now.Add(-MempoolTxTTL).Unix() {
		return fmt.Errorf("transaction expired: older than %s", MempoolTxTTL)
	}
	if tx.Timestamp > now.Add(maxTxFutureSkew).Unix() {
		return fmt.Errorf("transaction timestamp too far in the future (max skew %s)", maxTxFutureSkew)
	}

	sender, ok := state.Accounts[tx.From]
	if !ok {
		sender = &Account{Address: tx.From, Balance: 0, Nonce: 0}
	}
	for _, pending := range state.Mempool {
		if pending.From == tx.From && pending.Nonce == tx.Nonce {
			return fmt.Errorf("duplicate nonce in mempool for %s: already have tx %s", tx.From, pending.ID)
		}
	}
	if tx.Nonce != sender.Nonce+1 {
		return fmt.Errorf("nonce mismatch: expected %d, got %d", sender.Nonce+1, tx.Nonce)
	}
	if sender.Balance < tx.Amount+tx.Fee {
		return fmt.Errorf("insufficient balance: have %.8f, need %.8f", sender.Balance, tx.Amount+tx.Fee)
	}

	tx.ID = TxID(tx)

	// Insert into mempool ordered by fee (desc), then timestamp (asc), then nonce (asc), then ID.
	insertAt := sort.Search(len(state.Mempool), func(i int) bool {
		existing := state.Mempool[i]
		switch {
		case !FloatEqual(tx.Fee, existing.Fee):
			return tx.Fee > existing.Fee
		case tx.Timestamp != existing.Timestamp:
			return tx.Timestamp < existing.Timestamp
		case tx.Nonce != existing.Nonce:
			return tx.Nonce < existing.Nonce
		default:
			return tx.ID < existing.ID
		}
	})
	state.Mempool = append(state.Mempool, Transaction{})
	copy(state.Mempool[insertAt+1:], state.Mempool[insertAt:])
	state.Mempool[insertAt] = *tx

	if len(state.Mempool) > MempoolMaxSize {
		// Drop the lowest-priority transaction (last element).
		dropped := state.Mempool[len(state.Mempool)-1]
		state.Mempool = state.Mempool[:len(state.Mempool)-1]
		if dropped.ID == tx.ID {
			return fmt.Errorf("mempool full: tx rejected due to low priority")
		}
	}
	return nil
}
