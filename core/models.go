package core

import (
	"sync"
	"time"
)

// Chain / economics constants.
const (
	CoinName              = "CoCaX"
	Ticker                = "CoX"
	AddressPrefix         = "CoX"
	ChainID               = uint64(11121633)
	GenesisMessage        = "For united peoples working together. To hell with geography and borders, let us unite!"
	TotalSupplyCap        = 33_000_000.0
	BaseBlockReward       = 3.3
	HalvingInterval       = uint64(1_000_000)
	FixedFee              = 0.01
	FounderAddress        = "0x1111111111111111111111111111111111111111"
	FounderAllocation     = 3_300_000.0
	TargetBlockTime       = 30 * time.Second
	FinalizationThreshold = 3
)

// MempoolMaxSize sets an upper bound on pending transactions held in memory.
// Defined as a variable to permit tuning in tests.
var MempoolMaxSize = 1024

// Transaction represents a signed CoCaX transaction.
type Transaction struct {
	ID                 string            `json:"id"`
	From               string            `json:"from"`
	To                 string            `json:"to"`
	Amount             float64           `json:"amount"`
	Fee                float64           `json:"fee"`
	Nonce              uint64            `json:"nonce"`
	Timestamp          int64             `json:"timestamp"`
	PubKeyX            string            `json:"pub_key_x,omitempty"`
	PubKeyY            string            `json:"pub_key_y,omitempty"`
	SigR               string            `json:"sig_r,omitempty"`
	SigS               string            `json:"sig_s,omitempty"`
	IsCoinbase         bool              `json:"is_coinbase"`
	Memo               string            `json:"memo,omitempty"`
	SequenceNumber     uint64            `json:"sequence_number,omitempty"`
	VerifiedSignatures map[string]string `json:"verified_signatures,omitempty"` // peer_id -> signature
}

// Block represents a block in the CoCaX chain.
type Block struct {
	Index              uint64              `json:"index"`
	PrevHash           string              `json:"prev_hash"`
	Hash               string              `json:"hash"`
	Timestamp          int64               `json:"timestamp"`
	Transactions       []Transaction       `json:"transactions"`
	Reward             float64             `json:"reward"`
	Miner              string              `json:"miner"`
	Memo               string              `json:"memo,omitempty"`
	Verifications      []BlockVerification `json:"verifications,omitempty"`
	CrossSigs          map[string]string   `json:"cross_sigs,omitempty"` // optional peer signatures for PoVS
	Finalized          bool                `json:"finalized"`
	BlockVerifications map[string]bool     `json:"block_verifications,omitempty"`
	VerifiedSequence   []Verification      `json:"verified_sequence,omitempty"`
}

// BlockVerification captures a peer's validation vote for a block.
type BlockVerification struct {
	Peer     string `json:"peer"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// Verification represents a validator confirmation within the PoVS sequence.
type Verification struct {
	VerifierID string `json:"verifier_id"`
	Signature  []byte `json:"signature"`
	Index      int    `json:"index"`
}

// Account represents the on-chain state for a single address.
type Account struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
	Nonce   uint64  `json:"nonce"`
}

// ChainState holds the in-memory chain, account state and mempool.
type ChainState struct {
	mu           sync.RWMutex
	Chain        []Block             `json:"chain"`
	Accounts     map[string]*Account `json:"accounts"`
	MintedSupply float64             `json:"minted_supply"`
	Mempool      []Transaction       `json:"-"`
}

// Lock acquires the write lock.
func (cs *ChainState) Lock() { cs.mu.Lock() }

// Unlock releases the write lock.
func (cs *ChainState) Unlock() { cs.mu.Unlock() }

// RLock acquires the read lock.
func (cs *ChainState) RLock() { cs.mu.RLock() }

// RUnlock releases the read lock.
func (cs *ChainState) RUnlock() { cs.mu.RUnlock() }
