package core

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// BlockVerifier represents a remote (or local) verifier used for PoVS voting.
// Implementations must treat the provided block as read-only.
type BlockVerifier func(*Block) error

type verificationSnapshot struct {
	lastBlock    Block
	chain        []Block
	mintedSupply float64
	accounts     map[string]Account
}

// cloneAccountValues makes value copies of accounts to allow isolated
// verification simulations without mutating shared state.
func cloneAccountValues(src map[string]*Account) map[string]Account {
	dst := make(map[string]Account, len(src))
	for addr, acc := range src {
		if acc == nil {
			continue
		}
		dst[addr] = *acc
	}
	return dst
}

func cloneAccountValueMap(src map[string]Account) map[string]Account {
	dst := make(map[string]Account, len(src))
	for addr, acc := range src {
		dst[addr] = acc
	}
	return dst
}

func snapshotState(state *ChainState) verificationSnapshot {
	state.mu.RLock()
	defer state.mu.RUnlock()
	snap := verificationSnapshot{
		mintedSupply: state.MintedSupply,
		accounts:     cloneAccountValues(state.Accounts),
		chain:        append([]Block{}, state.Chain...),
	}
	if len(state.Chain) > 0 {
		snap.lastBlock = state.Chain[len(state.Chain)-1]
	}
	return snap
}

// snapshotStateLocked builds a snapshot while assuming the caller already holds
// the state lock.
func snapshotStateLocked(state *ChainState) verificationSnapshot {
	snap := verificationSnapshot{
		mintedSupply: state.MintedSupply,
		accounts:     cloneAccountValues(state.Accounts),
		chain:        append([]Block{}, state.Chain...),
	}
	if len(state.Chain) > 0 {
		snap.lastBlock = state.Chain[len(state.Chain)-1]
	}
	return snap
}

func verifyBlockAgainstSnapshot(snap verificationSnapshot, block *Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}
	expectedDifficulty := ExpectedDifficulty(snap.chain)
	gotDifficulty := clampDifficulty(block.Difficulty)
	if gotDifficulty != expectedDifficulty {
		return fmt.Errorf("difficulty mismatch: got %d want %d", gotDifficulty, expectedDifficulty)
	}
	if block.Hash != BlockHash(block) {
		return fmt.Errorf("block hash mismatch")
	}
	if !HashMeetsDifficulty(block.Hash, gotDifficulty) {
		return fmt.Errorf("block hash does not satisfy declared difficulty")
	}
	if snap.lastBlock.Hash != "" {
		if block.Index != snap.lastBlock.Index+1 {
			return fmt.Errorf("block index mismatch: got %d want %d", block.Index, snap.lastBlock.Index+1)
		}
		if block.PrevHash != snap.lastBlock.Hash {
			return fmt.Errorf("prev hash mismatch: got %s want %s", block.PrevHash, snap.lastBlock.Hash)
		}
	}
	if snap.mintedSupply+block.Reward > TotalSupplyCap {
		return fmt.Errorf("reward exceeds supply cap")
	}

	accounts := cloneAccountValueMap(snap.accounts)

	expectedSeq := uint64(0)
	for _, tx := range block.Transactions {
		if tx.IsCoinbase {
			if expectedSeq != 0 {
				return fmt.Errorf("coinbase must be the first transaction")
			}
			if tx.SequenceNumber != 0 {
				return fmt.Errorf("coinbase must have sequence 0")
			}
			expectedSeq = 1
			continue
		}
		if tx.SequenceNumber != expectedSeq {
			return fmt.Errorf("tx sequence mismatch: got %d want %d", tx.SequenceNumber, expectedSeq)
		}
		expectedSeq++
		if err := VerifyTransaction(&tx); err != nil {
			return fmt.Errorf("tx %s verification failed: %w", tx.ID, err)
		}
		sender := accounts[tx.From]
		if tx.Nonce != sender.Nonce+1 {
			return fmt.Errorf("nonce mismatch for %s: got %d want %d", tx.From, tx.Nonce, sender.Nonce+1)
		}
		if sender.Balance < tx.Amount+tx.Fee {
			return fmt.Errorf("insufficient balance for %s", tx.From)
		}
		sender.Balance -= tx.Amount + tx.Fee
		sender.Nonce = tx.Nonce
		accounts[tx.From] = sender

		receiver := accounts[tx.To]
		receiver.Address = tx.To
		receiver.Balance += tx.Amount
		accounts[tx.To] = receiver
	}
	return nil
}

// VerifyBlock validates a block against the current chain state without mutating it.
func VerifyBlock(state *ChainState, block *Block) error {
	snap := snapshotState(state)
	if snap.lastBlock.Hash == "" && block.Index != 0 {
		return fmt.Errorf("no genesis block found")
	}
	return verifyBlockAgainstSnapshot(snap, block)
}

// CreateBlockTemplate builds a block candidate from the current mempool without
// mutating state.
func CreateBlockTemplate(state *ChainState, miner string) (*Block, error) {
	state.mu.RLock()
	if len(state.Chain) == 0 {
		state.mu.RUnlock()
		return nil, fmt.Errorf("no genesis block found")
	}
	prev := state.Chain[len(state.Chain)-1]
	chain := make([]Block, len(state.Chain))
	copy(chain, state.Chain)
	mempool := make([]Transaction, len(state.Mempool))
	copy(mempool, state.Mempool)
	minted := state.MintedSupply
	state.mu.RUnlock()

	now := time.Now()
	blockIndex := prev.Index + 1
	reward := BlockReward(blockIndex)
	if minted+reward > TotalSupplyCap {
		reward = TotalSupplyCap - minted
		if reward < 0 {
			reward = 0
		}
	}

	coinbaseTx := Transaction{
		ID:             fmt.Sprintf("coinbase-%d", blockIndex),
		From:           "coinbase",
		To:             miner,
		Amount:         reward,
		Fee:            0,
		Nonce:          0,
		Timestamp:      now.Unix(),
		IsCoinbase:     true,
		SequenceNumber: 0,
	}

	txs := []Transaction{coinbaseTx}
	for i := range mempool {
		mempool[i].SequenceNumber = uint64(i + 1)
		txs = append(txs, mempool[i])
	}

	block := &Block{
		Index:        blockIndex,
		PrevHash:     prev.Hash,
		Timestamp:    now.Unix(),
		Difficulty:   ExpectedDifficulty(chain),
		Transactions: txs,
		Reward:       reward,
		Miner:        miner,
	}
	if err := MineBlockPoW(block); err != nil {
		return nil, err
	}
	return block, nil
}

// MineBlockPoW updates nonce/hash until block satisfies its configured difficulty.
func MineBlockPoW(block *Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}
	block.Difficulty = clampDifficulty(block.Difficulty)
	const maxPoWAttempts = uint64(10_000_000)
	for attempts := uint64(0); attempts < maxPoWAttempts; attempts++ {
		block.Hash = BlockHash(block)
		if HashMeetsDifficulty(block.Hash, block.Difficulty) {
			return nil
		}
		block.Nonce++
	}
	return fmt.Errorf("failed to mine block within %d attempts", maxPoWAttempts)
}

// DistributeRewards credits the miner while respecting the supply cap.
// It caps the reward to the remaining supply and updates minted supply to
// reflect the new issuance.
func DistributeRewards(state *ChainState, miner string, reward float64) {
	if reward <= 0 || miner == "" {
		return
	}
	available := TotalSupplyCap - state.MintedSupply
	if reward > available {
		reward = available
	}
	if _, ok := state.Accounts[miner]; !ok {
		state.Accounts[miner] = &Account{Address: miner}
	}
	state.Accounts[miner].Balance += reward
	state.MintedSupply += reward
}

// ApplyPenalty slashes the offender's balance and burns the slashed amount.
// The penalty is applied when a block is rejected by consensus; the slashed
// amount is deducted from the offender and removed from minted supply.
// applyPenaltyLocked assumes the caller already holds the state lock; the
// exported ApplyPenalty wrapper acquires the lock before invoking it.
func applyPenaltyLocked(state *ChainState, offender string, amount float64) {
	acc, ok := state.Accounts[offender]
	if !ok || amount <= 0 {
		return
	}
	if amount > acc.Balance {
		applied := acc.Balance
		log.Printf("[Consensus] penalty capped to available balance for %s: requested %.4f, applied %.4f", offender, amount, applied)
		amount = applied
	}
	acc.Balance -= amount
	if state.MintedSupply >= amount {
		state.MintedSupply -= amount
	}
}

func ApplyPenalty(state *ChainState, offender string, amount float64) {
	if offender == "" || amount <= 0 {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	applyPenaltyLocked(state, offender, amount)
}

// AddBlock runs PoVS-style parallel verification and, on success, commits the block.
// Local verification always counts as one vote; external verifiers are optional
// to allow single-node operation.
func AddBlock(state *ChainState, block *Block, dataDir string, verifiers ...BlockVerifier) error {
	var (
		wg            sync.WaitGroup
		mu            sync.Mutex
		votesAccepted int
		results       []BlockVerification
		firstErr      error
	)

	if block.BlockVerifications == nil {
		block.BlockVerifications = make(map[string]bool)
	}

	runVerifier := func(name string, fn BlockVerifier) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := fn(block)
			mu.Lock()
			defer mu.Unlock()
			res := BlockVerification{Peer: name, Accepted: err == nil}
			if err != nil {
				res.Reason = err.Error()
				if firstErr == nil {
					firstErr = err
				}
			}
			if res.Accepted {
				votesAccepted++
			}
			results = append(results, res)
		}()
	}

	localSnap := snapshotState(state)

	runVerifier("local", func(b *Block) error { return verifyBlockAgainstSnapshot(localSnap, b) })
	for i, v := range verifiers {
		name := fmt.Sprintf("peer-%d", i+1)
		runVerifier(name, v)
	}

	wg.Wait()
	// Guard against duplicate peers when a block already carries verification
	// metadata (e.g. received from the network) before local verifier results
	// are merged.
	existingPeers := make(map[string]struct{}, len(block.Verifications))
	merged := make([]BlockVerification, 0, len(block.Verifications)+len(results))
	for _, v := range block.Verifications {
		if _, seen := existingPeers[v.Peer]; seen {
			continue
		}
		existingPeers[v.Peer] = struct{}{}
		block.BlockVerifications[v.Peer] = v.Accepted
		merged = append(merged, v)
	}
	sortedResults := make([]BlockVerification, len(results))
	copy(sortedResults, results)
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].Peer < sortedResults[j].Peer
	})
	for _, r := range sortedResults {
		if _, seen := existingPeers[r.Peer]; seen {
			continue
		}
		block.BlockVerifications[r.Peer] = r.Accepted
		merged = append(merged, r)
	}
	block.Verifications = merged
	markFinalizedIfThresholdReached(block)

	totalVotes := len(verifiers) + 1
	majority := totalVotes/2 + 1
	if votesAccepted < majority {
		state.mu.Lock()
		applyPenaltyLocked(state, block.Miner, block.Reward)
		state.mu.Unlock()
		if firstErr != nil {
			return firstErr
		}
		return fmt.Errorf("block rejected by majority (%d/%d)", votesAccepted, totalVotes)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Re-verify using the latest state snapshot to avoid race conditions.
	if err := verifyBlockAgainstSnapshot(snapshotStateLocked(state), block); err != nil {
		return err
	}

	// Verification already enforced balances/nonces; ensure referenced accounts
	// still exist under the write lock before mutating state.
	for _, tx := range block.Transactions {
		if tx.IsCoinbase {
			continue
		}
		if state.Accounts[tx.From] == nil {
			return fmt.Errorf("sender account missing during apply: %s", tx.From)
		}
	}

	DistributeRewards(state, block.Miner, block.Reward)
	for _, tx := range block.Transactions {
		if tx.IsCoinbase {
			continue
		}
		from := state.Accounts[tx.From]
		from.Balance -= tx.Amount + tx.Fee
		from.Nonce = tx.Nonce
		state.Accounts[tx.From] = from

		to := state.Accounts[tx.To]
		if to == nil {
			to = &Account{Address: tx.To}
		}
		to.Balance += tx.Amount
		state.Accounts[tx.To] = to
	}

	state.Mempool = []Transaction{}
	state.Chain = append(state.Chain, *block)

	if dataDir != "" {
		if err := SaveState(dataDir, state); err != nil {
			log.Printf("[Mine] Failed to persist state: %v", err)
		}
	}

	return nil
}

// MineBlock builds and commits a new block from the current mempool using PoVS.
func MineBlock(state *ChainState, miner, dataDir string) (*Block, error) {
	block, err := CreateBlockTemplate(state, miner)
	if err != nil {
		return nil, err
	}
	if err := AddBlock(state, block, dataDir); err != nil {
		return nil, err
	}
	return block, nil
}
