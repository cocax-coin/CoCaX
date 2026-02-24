package rpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"

	"cocax-core/core"
)

// Server exposes the CoCaX RPC API (HTTP).
type Server struct {
	state   *core.ChainState
	dataDir string
	miner   string
	peers   []string
}

// smallSliceHint provides a modest preallocation to reduce reallocations when
// building transaction response lists. Tuned for typical wallet dashboard usage.
const smallSliceHint = 32

type txWithMeta struct {
	core.Transaction
	BlockIndex uint64 `json:"block_index,omitempty"`
	BlockHash  string `json:"block_hash,omitempty"`
	Status     string `json:"status"`
}

// NewServer creates a new RPC server.
func NewServer(state *core.ChainState, dataDir, miner string, peers ...string) *Server {
	return &Server{
		state:   state,
		dataDir: dataDir,
		miner:   miner,
		peers:   append([]string(nil), peers...),
	}
}

// Router builds and returns the HTTP handler with CORS middleware applied.
func (a *Server) Router() http.Handler {
	mux := http.NewServeMux()
	// Wallet-friendly aliases (with or without /api prefix).
	mux.HandleFunc("/balance", a.handleBalance)
	mux.HandleFunc("/balance/", a.handleBalance)
	mux.HandleFunc("/api/balance", a.handleBalance)
	mux.HandleFunc("/api/balance/", a.handleBalance)
	mux.HandleFunc("/transactions", a.handleTransactions)
	mux.HandleFunc("/transactions/", a.handleTransactions)
	mux.HandleFunc("/api/transactions", a.handleTransactions)
	mux.HandleFunc("/api/transactions/", a.handleTransactions)
	mux.HandleFunc("/tx/submit", a.handleTxSubmit)
	mux.HandleFunc("/blocks", a.handleBlocks)
	mux.HandleFunc("/status", a.handleStatus)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/audit", a.handleAudit)
	mux.HandleFunc("/api/audit", a.handleAudit)
	mux.HandleFunc("/mine", a.handleMine)
	mux.HandleFunc("/rpc", a.handleJSONRPC)
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

// extractAddress pulls an address from the query string (?address=) first, then
// falls back to trimming one of the provided path prefixes (e.g. /balance/{addr}).
// It returns an empty string when no address could be extracted. Callers are
// responsible for validating the returned address.
func extractAddress(r *http.Request, prefixes ...string) string {
	// Prefer explicit query parameter.
	if addr := strings.TrimSpace(r.URL.Query().Get("address")); addr != "" {
		return addr
	}
	path := r.URL.Path
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			addr := strings.TrimPrefix(path, p)
			addr = strings.TrimPrefix(addr, "/")
			if addr != "" {
				return addr
			}
		}
	}
	return ""
}

// handleBalance serves GET /balance/{address}.
func (a *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	address := extractAddress(r, "/balance", "/api/balance")
	if address == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing address"})
		return
	}
	if !isValidAddress(address) {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid address"})
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

// handleTransactions serves GET /transactions/{address} with pending and confirmed txs.
func (a *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	address := extractAddress(r, "/transactions", "/api/transactions")
	if address == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing address"})
		return
	}
	if !isValidAddress(address) {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid address"})
		return
	}

	// Acquire a read lock to filter the chain and mempool with a consistent view.
	a.state.RLock()
	chain := a.state.Chain
	mempool := a.state.Mempool
	confirmed := make([]txWithMeta, 0, smallSliceHint)
	for _, blk := range chain {
		for _, tx := range blk.Transactions {
			if tx.From == address || tx.To == address {
				confirmed = append(confirmed, txWithMeta{
					Transaction: tx,
					BlockIndex:  blk.Index,
					BlockHash:   blk.Hash,
					Status:      "confirmed",
				})
			}
		}
	}

	pending := make([]txWithMeta, 0, smallSliceHint)
	for _, tx := range mempool {
		if tx.From == address || tx.To == address {
			pending = append(pending, txWithMeta{
				Transaction: tx,
				Status:      "pending",
			})
		}
	}
	a.state.RUnlock()

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"address":   address,
		"confirmed": confirmed,
		"pending":   pending,
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
	for _, pending := range a.state.Mempool {
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

// handleStatus exposes a lightweight dashboard-friendly chain summary.
func (a *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	a.state.RLock()
	blocks := len(a.state.Chain)
	mempool := len(a.state.Mempool)
	minted := a.state.MintedSupply
	txCount := 0
	var latest core.Block
	if blocks > 0 {
		latest = a.state.Chain[blocks-1]
		for _, blk := range a.state.Chain {
			txCount += len(blk.Transactions)
		}
	}
	a.state.RUnlock()

	resp := map[string]interface{}{
		"chain_id":         core.ChainID,
		"blocks":           blocks,
		"transactions":     txCount,
		"mempool_size":     mempool,
		"minted_supply":    minted,
		"peers_configured": len(a.peers),
	}
	if blocks > 0 {
		resp["latest_block"] = map[string]interface{}{
			"index":     latest.Index,
			"hash":      latest.Hash,
			"timestamp": latest.Timestamp,
			"txs":       len(latest.Transactions),
		}
	}
	jsonResponse(w, http.StatusOK, resp)
}

// handleAudit returns per-block verification metadata to aid debugging.
func (a *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.state.RLock()
	chain := make([]core.Block, len(a.state.Chain))
	copy(chain, a.state.Chain)
	a.state.RUnlock()

	audit := make([]map[string]interface{}, 0, len(chain))
	for _, blk := range chain {
		audit = append(audit, map[string]interface{}{
			"index":               blk.Index,
			"hash":                blk.Hash,
			"prev_hash":           blk.PrevHash,
			"timestamp":           blk.Timestamp,
			"txs":                 len(blk.Transactions),
			"verifications":       blk.Verifications,
			"block_verifications": blk.BlockVerifications,
		})
	}
	jsonResponse(w, http.StatusOK, audit)
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

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

var balancePrecisionWarn sync.Once

func hexifyUint64(v uint64) string {
	return fmt.Sprintf("0x%x", v)
}

func isValidAddress(addr string) bool {
	if addr == "" {
		return false
	}
	if strings.HasPrefix(addr, "0x") {
		decoded, err := decodeHexData(addr)
		return err == nil && len(decoded) == 20
	}
	if strings.HasPrefix(addr, core.AddressPrefix) {
		decoded, err := decodeHexData(addr[len(core.AddressPrefix):])
		return err == nil && len(decoded) == 20
	}
	return false
}

func decodeHexData(input string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(input, "0x"))
}

func balanceToHexWei(balance float64) (string, error) {
	// Defensive guard: balances should not be negative; treat as an error if encountered.
	if balance < 0 {
		return "", fmt.Errorf("negative balance")
	}
	if balance == 0 {
		return "0x0", nil
	}
	scale := big.NewFloat(1e18)
	weiFloat := new(big.Float).Mul(big.NewFloat(balance), scale)
	weiInt, acc := weiFloat.Int(nil)
	weiHex := weiInt.Text(16)
	if acc != big.Exact {
		balancePrecisionWarn.Do(func() {
			log.Printf("[RPC] balance conversion rounded to wei; precision may be reduced when exceeding 18 decimals (accuracy=%v)", acc)
		})
	}
	return "0x" + weiHex, nil
}

// handleJSONRPC serves POST /rpc (JSON-RPC 2.0) for MetaMask-style compatibility.
func (a *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "failed to parse JSON-RPC request"},
			ID:      nil,
		})
		return
	}

	res := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "net_version":
		res.Result = fmt.Sprintf("%d", core.ChainID)
	case "eth_chainId":
		res.Result = hexifyUint64(core.ChainID)
	case "eth_blockNumber":
		a.state.RLock()
		height := uint64(0)
		if len(a.state.Chain) > 0 {
			height = a.state.Chain[len(a.state.Chain)-1].Index
		}
		a.state.RUnlock()
		res.Result = hexifyUint64(height)
	case "eth_getBalance":
		var params []string
		if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
			res.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		address := strings.TrimSpace(params[0])
		if !isValidAddress(address) {
			res.Error = &rpcError{Code: -32602, Message: "invalid address"}
			break
		}
		a.state.RLock()
		acc, ok := a.state.Accounts[address]
		var balance float64
		if ok && acc != nil {
			balance = acc.Balance
		}
		a.state.RUnlock()
		weiHex, err := balanceToHexWei(balance)
		if err != nil {
			res.Error = &rpcError{Code: -32000, Message: err.Error()}
			break
		}
		res.Result = weiHex
	case "eth_sendRawTransaction":
		var params []string
		if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
			res.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		rawBytes, err := decodeHexData(params[0])
		if err != nil {
			res.Error = &rpcError{Code: -32602, Message: "invalid raw transaction"}
			break
		}
		var tx core.Transaction
		if err := json.Unmarshal(rawBytes, &tx); err != nil {
			res.Error = &rpcError{Code: -32602, Message: "unable to decode transaction"}
			break
		}
		if err := a.validateAndAddTx(&tx); err != nil {
			res.Error = &rpcError{Code: -32000, Message: err.Error()}
			break
		}
		res.Result = tx.ID
	default:
		res.Error = &rpcError{Code: -32601, Message: "method not found"}
	}

	jsonResponse(w, http.StatusOK, res)
}
