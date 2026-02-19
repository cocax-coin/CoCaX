package main

import (
	"sync"
	"time"
)

// Chain / economics constants.
const (
	CoinName          = "CoCaX"
	Ticker            = "CoX"
	AddressPrefix     = "CoX"
	GenesisMessage    = "For united peoples working together. To hell with geography and borders, let us unite!"
	TotalSupplyCap    = 33_000_000.0
	BaseBlockReward   = 3.3
	HalvingInterval   = uint64(1_000_000)
	FixedFee          = 0.01
	FounderAddress    = "CoX_FOUNDER_PLACEHOLDER_UPDATE_ME"
	FounderAllocation = 3_300_000.0
	TargetBlockTime   = 30 * time.Second
)

// Transaction represents a signed CoCaX transaction.
type Transaction struct {
	ID         string  `json:"id"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Amount     float64 `json:"amount"`
	Fee        float64 `json:"fee"`
	Nonce      uint64  `json:"nonce"`
	Timestamp  int64   `json:"timestamp"`
	PubKeyX    string  `json:"pub_key_x,omitempty"`
	PubKeyY    string  `json:"pub_key_y,omitempty"`
	SigR       string  `json:"sig_r,omitempty"`
	SigS       string  `json:"sig_s,omitempty"`
	IsCoinbase bool    `json:"is_coinbase"`
	Memo       string  `json:"memo,omitempty"`
}

// TimedCommitment represents a lightweight PoTC commitment stored in a block.
type TimedCommitment struct {
	Validator      string `json:"validator"`
	CommitTime     int64  `json:"commit_time"`
	RevealDeadline int64  `json:"reveal_deadline"`
	Window         int64  `json:"window"`
	Nonce          string `json:"nonce"`
}

// Block represents a block in the CoCaX chain.
type Block struct {
	Index        uint64          `json:"index"`
	PrevHash     string          `json:"prev_hash"`
	Hash         string          `json:"hash"`
	Timestamp    int64           `json:"timestamp"`
	Transactions []Transaction   `json:"transactions"`
	Commitment   TimedCommitment `json:"commitment"`
	Reward       float64         `json:"reward"`
	Miner        string          `json:"miner"`
	Memo         string          `json:"memo,omitempty"`
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
