package core

import (
	"fmt"
	"log"
	"time"
)

// MineBlock builds and commits a new block from the current mempool.
// It applies coinbase reward (respecting supply cap and halving) and executes
// all pending mempool transactions against the account state.
func MineBlock(state *ChainState, miner, dataDir string) (*Block, error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	if len(state.Chain) == 0 {
		return nil, fmt.Errorf("no genesis block found")
	}

	prev := state.Chain[len(state.Chain)-1]
	now := time.Now()
	blockIndex := prev.Index + 1
	reward := BlockReward(blockIndex)

	// Enforce supply cap.
	if state.MintedSupply+reward > TotalSupplyCap {
		reward = TotalSupplyCap - state.MintedSupply
		if reward < 0 {
			reward = 0
		}
	}

	window := int64(TargetBlockTime.Seconds())
	commitment := TimedCommitment{
		Validator:      miner,
		CommitTime:     now.Unix(),
		RevealDeadline: now.Unix() + window,
		Window:         window,
		Nonce:          fmt.Sprintf("%d", now.UnixNano()),
	}

	// Reject if reveal deadline has already passed (defensive; window is future).
	if now.Unix() > commitment.RevealDeadline {
		return nil, fmt.Errorf("reveal deadline already passed")
	}

	coinbaseTx := Transaction{
		ID:         fmt.Sprintf("coinbase-%d", blockIndex),
		From:       "coinbase",
		To:         miner,
		Amount:     reward,
		Fee:        0,
		Nonce:      0,
		Timestamp:  now.Unix(),
		IsCoinbase: true,
	}

	txs := []Transaction{coinbaseTx}
	txs = append(txs, state.Mempool...)

	block := Block{
		Index:        blockIndex,
		PrevHash:     prev.Hash,
		Timestamp:    now.Unix(),
		Transactions: txs,
		Commitment:   commitment,
		Reward:       reward,
		Miner:        miner,
	}
	block.Hash = BlockHash(&block)

	// Credit miner reward.
	if reward > 0 && miner != "" {
		if _, ok := state.Accounts[miner]; !ok {
			state.Accounts[miner] = &Account{Address: miner}
		}
		state.Accounts[miner].Balance += reward
		state.MintedSupply += reward
	}

	// Apply mempool transactions.
	for _, tx := range state.Mempool {
		from := state.Accounts[tx.From]
		if from == nil {
			continue
		}
		from.Balance -= tx.Amount + tx.Fee
		from.Nonce = tx.Nonce
		if _, ok := state.Accounts[tx.To]; !ok {
			state.Accounts[tx.To] = &Account{Address: tx.To}
		}
		state.Accounts[tx.To].Balance += tx.Amount
	}

	state.Mempool = []Transaction{}
	state.Chain = append(state.Chain, block)

	if dataDir != "" {
		if err := SaveState(dataDir, state); err != nil {
			log.Printf("[Mine] Failed to persist state: %v", err)
		}
	}

	return &block, nil
}
