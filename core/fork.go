package core

import "fmt"

// ResolveFork attempts to replace the current chain tip with a finalized
// candidate block that builds on the same parent as the existing head. It
// returns true when the candidate was adopted.
func ResolveFork(state *ChainState, candidate *Block, dataDir string) (bool, error) {
	if state == nil || candidate == nil {
		return false, fmt.Errorf("state or candidate is nil")
	}

	state.RLock()
	chainLen := len(state.Chain)
	if chainLen < 2 {
		state.RUnlock()
		return false, nil
	}
	tip := state.Chain[chainLen-1]
	parent := state.Chain[chainLen-2]
	mempoolCopy := make([]Transaction, len(state.Mempool))
	copy(mempoolCopy, state.Mempool)
	state.RUnlock()

	if candidate.Index != tip.Index {
		return false, nil
	}
	if candidate.Hash == tip.Hash {
		return false, nil
	}
	if candidate.PrevHash != parent.Hash {
		return false, nil
	}
	if tip.Finalized && !candidate.Finalized {
		return false, nil
	}
	if !candidate.Finalized {
		return false, nil
	}

	baseChain := append([]Block(nil), state.Chain[:chainLen-1]...)
	rebuilt, err := rebuildStateFromChain(baseChain)
	if err != nil {
		return false, err
	}

	if err := AddBlock(rebuilt, candidate, dataDir); err != nil {
		return false, err
	}

	state.Lock()
	state.Chain = rebuilt.Chain
	state.Accounts = rebuilt.Accounts
	state.MintedSupply = rebuilt.MintedSupply
	state.Mempool = mempoolCopy
	state.Unlock()
	return true, nil
}

func rebuildStateFromChain(chain []Block) (*ChainState, error) {
	cs := &ChainState{
		Accounts: make(map[string]*Account),
		Mempool:  []Transaction{},
	}
	founderAddr := FounderAddress
	if len(chain) > 0 && len(chain[0].Transactions) > 0 && chain[0].Transactions[0].To != "" {
		founderAddr = chain[0].Transactions[0].To
	}
	cs.Accounts[founderAddr] = &Account{Address: founderAddr, Balance: FounderAllocation}
	cs.MintedSupply = FounderAllocation

	for i := range chain {
		blk := chain[i]
		snap := verificationSnapshot{
			mintedSupply: cs.MintedSupply,
			accounts:     cloneAccountValues(cs.Accounts),
		}
		if len(cs.Chain) > 0 {
			snap.lastBlock = cs.Chain[len(cs.Chain)-1]
		}
		if err := verifyBlockAgainstSnapshot(snap, &blk); err != nil {
			return nil, err
		}

		DistributeRewards(cs, blk.Miner, blk.Reward)
		for _, tx := range blk.Transactions {
			if tx.IsCoinbase {
				continue
			}
			from := cs.Accounts[tx.From]
			if from == nil {
				from = &Account{Address: tx.From}
			}
			from.Balance -= tx.Amount + tx.Fee
			from.Nonce = tx.Nonce
			cs.Accounts[tx.From] = from

			to := cs.Accounts[tx.To]
			if to == nil {
				to = &Account{Address: tx.To}
			}
			to.Balance += tx.Amount
			cs.Accounts[tx.To] = to
		}
		cs.Chain = append(cs.Chain, blk)
	}
	return cs, nil
}
