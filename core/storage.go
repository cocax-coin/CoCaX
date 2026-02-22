package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type persistedState struct {
	Accounts     map[string]*Account `json:"accounts"`
	MintedSupply float64             `json:"minted_supply"`
}

// LoadState loads chain + account state from disk.
// If no persisted state exists it creates the genesis block and initialises the
// founder allocation, then saves to disk.
// founderAddr overrides the default FounderAddress constant when provided (non-empty).
func LoadState(dataDir, founderAddr string) (*ChainState, error) {
	if founderAddr == "" {
		founderAddr = FounderAddress
	}
	blocksPath := filepath.Join(dataDir, "blocks.json")
	statePath := filepath.Join(dataDir, "state.json")

	cs := &ChainState{
		Accounts: make(map[string]*Account),
		Mempool:  []Transaction{},
	}

	blocksData, blocksErr := os.ReadFile(blocksPath)
	stateData, stateErr := os.ReadFile(statePath)

	if blocksErr == nil && stateErr == nil {
		if err := json.Unmarshal(blocksData, &cs.Chain); err != nil {
			return nil, fmt.Errorf("failed to load blocks: %w", err)
		}
		var ps persistedState
		if err := json.Unmarshal(stateData, &ps); err != nil {
			return nil, fmt.Errorf("failed to load state: %w", err)
		}
		cs.Accounts = ps.Accounts
		cs.MintedSupply = ps.MintedSupply
		return cs, nil
	}

	// First run: create data directory, genesis block and founder allocation.
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	genesis := buildGenesisBlock(founderAddr)
	cs.Chain = []Block{genesis}
	cs.Accounts[founderAddr] = &Account{
		Address: founderAddr,
		Balance: FounderAllocation,
		Nonce:   0,
	}
	cs.MintedSupply = FounderAllocation

	if err := SaveState(dataDir, cs); err != nil {
		return nil, fmt.Errorf("failed to save genesis state: %w", err)
	}
	return cs, nil
}

// SaveState writes the current chain and account state to disk.
// The caller is responsible for holding any required locks before calling.
func SaveState(dataDir string, cs *ChainState) error {
	blocksPath := filepath.Join(dataDir, "blocks.json")
	statePath := filepath.Join(dataDir, "state.json")

	blocksData, err := json.MarshalIndent(cs.Chain, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(blocksPath, blocksData, 0644); err != nil {
		return err
	}

	ps := persistedState{
		Accounts:     cs.Accounts,
		MintedSupply: cs.MintedSupply,
	}
	stateData, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, stateData, 0644)
}

// buildGenesisBlock constructs the genesis block.
// founderAddr is the address that receives the genesis coinbase (for record keeping).
func buildGenesisBlock(founderAddr string) Block {
	now := time.Now().Unix()
	genesisTx := Transaction{
		ID:         "genesis-coinbase",
		From:       "coinbase",
		To:         founderAddr,
		Amount:     0,
		Fee:        0,
		Nonce:      0,
		Timestamp:  now,
		IsCoinbase: true,
		Memo:       GenesisMessage,
	}
	commitment := TimedCommitment{
		Validator:      "genesis",
		CommitTime:     now,
		RevealDeadline: now + int64(TargetBlockTime.Seconds()),
		Window:         int64(TargetBlockTime.Seconds()),
		Nonce:          "genesis_nonce_0",
	}
	block := Block{
		Index:        0,
		PrevHash:     "0000000000000000000000000000000000000000000000000000000000000000",
		Timestamp:    now,
		Transactions: []Transaction{genesisTx},
		Commitment:   commitment,
		Reward:       0,
		Miner:        "genesis",
		Memo:         GenesisMessage,
	}
	block.Hash = BlockHash(&block)
	return block
}
