package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// APIServer exposes the CoCaX HTTP API.
type APIServer struct {
	state   *ChainState
	dataDir string
	miner   string
}

// NewAPIServer creates a new APIServer.
func NewAPIServer(state *ChainState, dataDir, miner string) *APIServer {
	return &APIServer{state: state, dataDir: dataDir, miner: miner}
}

// Router builds and returns the HTTP handler with CORS middleware applied.
func (a *APIServer) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/balance/", a.handleBalance)
	mux.HandleFunc("/tx/submit", a.handleTxSubmit)
	mux.HandleFunc("/blocks", a.handleBlocks)
	mux.HandleFunc("/mine", a.handleMine)
	mux.HandleFunc("/", a.handleStatic)
	return corsMiddleware(mux)
}

// corsMiddleware adds CORS headers and handles OPTIONS pre-flight requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonResponse(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[API] json encode error: %v", err)
	}
}

// handleBalance serves GET /balance/{address}.
func (a *APIServer) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	address := strings.TrimPrefix(r.URL.Path, "/balance/")
	if address == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing address"})
		return
	}
	a.state.mu.RLock()
	acc, ok := a.state.Accounts[address]
	a.state.mu.RUnlock()
	if !ok {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"address": address,
			"balance": 0.0,
			"nonce":   uint64(0),
		})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"address": acc.Address,
		"balance": acc.Balance,
		"nonce":   acc.Nonce,
	})
}

// handleTxSubmit serves POST /tx/submit.
func (a *APIServer) handleTxSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var tx Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := a.validateAndAddTx(&tx); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	a.state.mu.RLock()
	mempoolSize := len(a.state.Mempool)
	a.state.mu.RUnlock()
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":       "accepted",
		"mempool_size": mempoolSize,
		"tx_id":        tx.ID,
	})
}

// validateAndAddTx validates all rules and appends tx to the mempool.
func (a *APIServer) validateAndAddTx(tx *Transaction) error {
	if tx.IsCoinbase {
		return fmt.Errorf("cannot submit coinbase transaction")
	}
	if !floatEqual(tx.Fee, FixedFee) {
		return fmt.Errorf("fee must be exactly %.2f CoX", FixedFee)
	}
	if err := VerifyTransaction(tx); err != nil {
		return fmt.Errorf("signature validation failed: %w", err)
	}

	a.state.mu.Lock()
	defer a.state.mu.Unlock()

	sender, ok := a.state.Accounts[tx.From]
	if !ok {
		sender = &Account{Address: tx.From, Balance: 0, Nonce: 0}
	}
	if tx.Nonce != sender.Nonce+1 {
		return fmt.Errorf("nonce mismatch: expected %d, got %d", sender.Nonce+1, tx.Nonce)
	}
	if sender.Balance < tx.Amount+tx.Fee {
		return fmt.Errorf("insufficient balance: have %.8f, need %.8f", sender.Balance, tx.Amount+tx.Fee)
	}

	tx.ID = TxID(tx)
	a.state.Mempool = append(a.state.Mempool, *tx)
	return nil
}

// handleBlocks serves GET /blocks.
func (a *APIServer) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.state.mu.RLock()
	chain := make([]Block, len(a.state.Chain))
	copy(chain, a.state.Chain)
	a.state.mu.RUnlock()
	jsonResponse(w, http.StatusOK, chain)
}

// handleMine serves POST /mine – mines a single block from the current mempool.
func (a *APIServer) handleMine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	block, err := MineBlock(a.state, a.miner, a.dataDir)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, block)
}

// handleStatic serves the wallet UI files from the wallet/ directory.
func (a *APIServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	// Safety: only serve files under wallet/ and reject path traversal.
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, filepath.Join("wallet", clean))
}

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
