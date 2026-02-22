package rpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"cocax-core/core"
)

// Server exposes the CoCaX RPC API (HTTP).
type Server struct {
	state   *core.ChainState
	dataDir string
	miner   string
}

// NewServer creates a new RPC server.
func NewServer(state *core.ChainState, dataDir, miner string) *Server {
	return &Server{state: state, dataDir: dataDir, miner: miner}
}

// Router builds and returns the HTTP handler with CORS middleware applied.
func (a *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/balance/", a.handleBalance)
	mux.HandleFunc("/tx/submit", a.handleTxSubmit)
	mux.HandleFunc("/blocks", a.handleBlocks)
	mux.HandleFunc("/mine", a.handleMine)
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
func (a *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	address := strings.TrimPrefix(r.URL.Path, "/balance/")
	if address == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing address"})
		return
	}
	a.state.RLock()
	acc, ok := a.state.Accounts[address]
	a.state.RUnlock()
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
func (a *Server) handleTxSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := a.validateAndAddTx(&tx); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	a.state.RLock()
	mempoolSize := len(a.state.Mempool)
	a.state.RUnlock()
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":       "accepted",
		"mempool_size": mempoolSize,
		"tx_id":        tx.ID,
	})
}

// validateAndAddTx validates all rules and appends tx to the mempool.
func (a *Server) validateAndAddTx(tx *core.Transaction) error {
	if tx.IsCoinbase {
		return fmt.Errorf("cannot submit coinbase transaction")
	}
	if !core.FloatEqual(tx.Fee, core.FixedFee) {
		return fmt.Errorf("fee must be exactly %.2f CoX", core.FixedFee)
	}
	if err := core.VerifyTransaction(tx); err != nil {
		return fmt.Errorf("signature validation failed: %w", err)
	}

	a.state.Lock()
	defer a.state.Unlock()

	sender, ok := a.state.Accounts[tx.From]
	if !ok {
		sender = &core.Account{Address: tx.From, Balance: 0, Nonce: 0}
	}
	if tx.Nonce != sender.Nonce+1 {
		return fmt.Errorf("nonce mismatch: expected %d, got %d", sender.Nonce+1, tx.Nonce)
	}
	if sender.Balance < tx.Amount+tx.Fee {
		return fmt.Errorf("insufficient balance: have %.8f, need %.8f", sender.Balance, tx.Amount+tx.Fee)
	}

	tx.ID = core.TxID(tx)
	a.state.Mempool = append(a.state.Mempool, *tx)
	return nil
}

// ValidateAndAddTx is an exported wrapper used by external callers (tests, tooling).
func (a *Server) ValidateAndAddTx(tx *core.Transaction) error {
	return a.validateAndAddTx(tx)
}

// handleBlocks serves GET /blocks.
func (a *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.state.RLock()
	chain := make([]core.Block, len(a.state.Chain))
	copy(chain, a.state.Chain)
	a.state.RUnlock()
	jsonResponse(w, http.StatusOK, chain)
}

// handleMine serves POST /mine – mines a single block from the current mempool.
func (a *Server) handleMine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	block, err := core.MineBlock(a.state, a.miner, a.dataDir)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, block)
}
