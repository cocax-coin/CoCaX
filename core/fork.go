package core

import "fmt"

// ResolveFork attempts to replace the current chain tip with a better-confirmed
// candidate block that builds on the same parent as the existing head. It
// prefers finalized candidates and, when not finalized, requires strictly more
// accepted validator confirmations than the current tip. It returns true when
// the candidate was adopted.
func ResolveFork(state *ChainState, candidate *Block, dataDir string) (bool, error) {
	if state == nil || candidate == nil {
		return false, fmt.Errorf("state or candidate is nil")
	}

	countAccepted := func(b Block) int {
		accepted := 0
		for _, v := range b.Verifications {
			if v.Accepted {
				accepted++
			}
		}
		return accepted
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
	tipAccepted := countAccepted(tip)
	candidateAccepted := countAccepted(*candidate)
	if tip.Finalized && !candidate.Finalized {
		return false, nil
	}
	if candidate.Finalized {
		if tip.Finalized && candidateAccepted < tipAccepted {
			return false, nil
		}
	} else if candidateAccepted <= tipAccepted {
		return false, nil
	}
	seenPeers := make(map[string]struct{}, len(candidate.Verifications))
	for _, v := range candidate.Verifications {
		if v.Peer == "" {
			return false, fmt.Errorf("candidate verification missing peer id")
		}
		if _, dup := seenPeers[v.Peer]; dup {
			return false, fmt.Errorf("duplicate verification from %s", v.Peer)
		}
		seenPeers[v.Peer] = struct{}{}
	}

	baseChain := make([]Block, chainLen-1)
	copy(baseChain, state.Chain[:chainLen-1])
	rebuilt, err := rebuildStateFromChain(baseChain)
	if err != nil {
		return false, err
	}

	if err := VerifyBlock(rebuilt, candidate); err != nil {
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
	// Default to the configured founder but prefer the address encoded in the
	// genesis transaction when available so replayed state matches persisted
	// chain metadata.
	founderAddr := FounderAddress
	if len(chain) > 0 {
		genesis := chain[0]
		if len(genesis.Transactions) > 0 && genesis.Transactions[0].To != "" {
			founderAddr = genesis.Transactions[0].To
		}
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
				return nil, fmt.Errorf("missing sender account during rebuild: %s", tx.From)
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
