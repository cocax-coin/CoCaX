package rpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
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

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

func hexifyUint64(v uint64) string {
	return fmt.Sprintf("0x%x", v)
}

func balanceToHexWei(balance float64) string {
	if balance <= 0 {
		return "0x0"
	}
	f := big.NewFloat(balance)
	f.Mul(f, big.NewFloat(1e18))
	i, _ := f.Int(nil)
	return "0x" + i.Text(16)
}

// handleJSONRPC serves POST /rpc (JSON-RPC 2.0) for MetaMask-style compatibility.
func (a *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	res := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch strings.ToLower(req.Method) {
	case "eth_chainid":
		res.Result = hexifyUint64(core.ChainID)
	case "eth_blocknumber":
		a.state.RLock()
		height := uint64(0)
		if len(a.state.Chain) > 0 {
			height = a.state.Chain[len(a.state.Chain)-1].Index
		}
		a.state.RUnlock()
		res.Result = hexifyUint64(height)
	case "eth_getbalance":
		var params []string
		if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
			res.Error = map[string]interface{}{
				"code":    -32602,
				"message": "invalid params",
			}
			break
		}
		address := params[0]
		a.state.RLock()
		acc, ok := a.state.Accounts[address]
		a.state.RUnlock()
		var balance float64
		if ok {
			balance = acc.Balance
		}
		res.Result = balanceToHexWei(balance)
	case "eth_sendrawtransaction":
		var params []string
		if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
			res.Error = map[string]interface{}{
				"code":    -32602,
				"message": "invalid params",
			}
			break
		}
		raw := strings.TrimPrefix(params[0], "0x")
		rawBytes, err := hex.DecodeString(raw)
		if err != nil {
			res.Error = map[string]interface{}{
				"code":    -32602,
				"message": "invalid raw transaction",
			}
			break
		}
		var tx core.Transaction
		if err := json.Unmarshal(rawBytes, &tx); err != nil {
			res.Error = map[string]interface{}{
				"code":    -32602,
				"message": "unable to decode transaction",
			}
			break
		}
		if err := a.validateAndAddTx(&tx); err != nil {
			res.Error = map[string]interface{}{
				"code":    -32000,
				"message": err.Error(),
			}
			break
		}
		res.Result = tx.ID
	default:
		res.Error = map[string]interface{}{
			"code":    -32601,
			"message": "method not found",
		}
	}

	jsonResponse(w, http.StatusOK, res)
}
