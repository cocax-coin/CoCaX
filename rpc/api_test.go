package rpc_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cocax-core/core"
	"cocax-core/rpc"
)

func newTestServer(t *testing.T) (*httptest.Server, *core.ChainState) {
	t.Helper()
	dir := t.TempDir()
	cs, err := core.LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	api := rpc.NewServer(cs, dir, core.FounderAddress)
	srv := httptest.NewServer(api.Router())
	t.Cleanup(srv.Close)
	return srv, cs
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      int         `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
	ID      int             `json:"id"`
}

// ---- CORS -------------------------------------------------------------------

func TestCORSPreflight_Returns204(t *testing.T) {
	srv, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/tx/submit", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 NoContent, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing Access-Control-Allow-Origin: *")
	}
}

func TestCORSHeaders_OnGet(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/blocks")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS header missing on GET /blocks")
	}
}

// ---- Balance endpoint -------------------------------------------------------

func TestBalanceEndpoint_UnknownAddress(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/balance/CoX0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["address"]; !ok {
		t.Error("response missing 'address' field")
	}
	if _, ok := body["balance"]; !ok {
		t.Error("response missing 'balance' field")
	}
	if _, ok := body["nonce"]; !ok {
		t.Error("response missing 'nonce' field")
	}
}

func TestBalanceEndpoint_KnownAddress(t *testing.T) {
	srv, cs := newTestServer(t)

	// Fund a fresh account directly.
	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 42.5, Nonce: 3}

	resp, err := http.Get(srv.URL + "/balance/" + addr)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["balance"] != 42.5 {
		t.Errorf("balance: want 42.5, got %v", body["balance"])
	}
	if body["nonce"] != float64(3) {
		t.Errorf("nonce: want 3, got %v", body["nonce"])
	}
}

// ---- /blocks endpoint -------------------------------------------------------

func TestBlocksEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/blocks")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var blocks []core.Block
	if err := json.NewDecoder(resp.Body).Decode(&blocks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(blocks) == 0 {
		t.Error("expected at least genesis block")
	}
}

// ---- /tx/submit -------------------------------------------------------------

func submitTx(t *testing.T, srvURL string, tx core.Transaction) (*http.Response, error) {
	t.Helper()
	body, err := json.Marshal(tx)
	if err != nil {
		return nil, err
	}
	return http.Post(srvURL+"/tx/submit", "application/json", bytes.NewReader(body))
}

func callJSONRPC(t *testing.T, srvURL, method string, params interface{}) jsonRPCResponse {
	t.Helper()
	payload, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	})
	if err != nil {
		t.Fatalf("marshal rpc request: %v", err)
	}
	resp, err := http.Post(srvURL+"/rpc", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("rpc request failed: %v", err)
	}
	defer resp.Body.Close()
	var out jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	return out
}

func TestTxSubmit_ValidTx(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 10.0, Nonce: 0}

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "accepted" {
		t.Errorf("expected status 'accepted', got %v", body["status"])
	}
}

func TestTxSubmit_MissingSignature(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 10.0, Nonce: 0}

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
		// No signature fields set.
	}

	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing signature, got %d", resp.StatusCode)
	}
}

func TestTxSubmit_InvalidSignature(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 10.0, Nonce: 0}

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)
	// Tamper after signing to force signature failure.
	tx.Amount = 2.0

	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid signature, got %d", resp.StatusCode)
	}
}

func TestTxSubmit_WrongFee(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 10.0, Nonce: 0}

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       0.1, // wrong
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for wrong fee, got %d", resp.StatusCode)
	}
}

func TestTxSubmit_DuplicateNonce(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 10.0, Nonce: 0}

	makeTx := func(amount float64) core.Transaction {
		tx := core.Transaction{
			From:      addr,
			To:        "CoXrecipient000000000000000000000000000000000",
			Amount:    amount,
			Fee:       core.FixedFee,
			Nonce:     1,
			Timestamp: time.Now().Unix(),
		}
		_ = core.SignTransaction(&tx, priv)
		return tx
	}

	firstTx := makeTx(1.0)
	if resp, err := submitTx(t, srv.URL, firstTx); err != nil {
		t.Fatalf("first submit failed: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected first tx accepted, got %d", resp.StatusCode)
		}
	}

	secondTx := makeTx(2.0)
	resp, err := submitTx(t, srv.URL, secondTx)
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for duplicate nonce, got %d", resp.StatusCode)
	}
}

func TestTxSubmit_NonceMismatch(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 10.0, Nonce: 0}

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     99, // should be 1
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for nonce mismatch, got %d", resp.StatusCode)
	}
}

func TestTxSubmit_CoinbaseRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	tx := core.Transaction{
		From:       "coinbase",
		To:         "CoXsomeone",
		Amount:     3.3,
		Fee:        0,
		IsCoinbase: true,
		Nonce:      1,
		Timestamp:  time.Now().Unix(),
	}
	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for coinbase tx, got %d", resp.StatusCode)
	}
}

// ---- JSON-RPC (/rpc) ---------------------------------------------------------

func TestJSONRPC_EthChainID(t *testing.T) {
	srv, _ := newTestServer(t)
	res := callJSONRPC(t, srv.URL, "eth_chainId", []string{})
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	var chainID string
	if err := json.Unmarshal(res.Result, &chainID); err != nil {
		t.Fatalf("unmarshal chainId: %v", err)
	}
	if chainID != "0xa9b3e1" {
		t.Fatalf("chain id mismatch: %s", chainID)
	}
}

func TestJSONRPC_NetVersion(t *testing.T) {
	srv, _ := newTestServer(t)
	res := callJSONRPC(t, srv.URL, "net_version", []string{})
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	var version string
	if err := json.Unmarshal(res.Result, &version); err != nil {
		t.Fatalf("unmarshal net_version: %v", err)
	}
	if version != fmt.Sprintf("%d", core.ChainID) {
		t.Fatalf("net_version mismatch: %s", version)
	}
}

func TestJSONRPC_EthBlockNumber(t *testing.T) {
	srv, _ := newTestServer(t)
	res := callJSONRPC(t, srv.URL, "eth_blockNumber", []string{})
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	var blockNum string
	if err := json.Unmarshal(res.Result, &blockNum); err != nil {
		t.Fatalf("unmarshal block number: %v", err)
	}
	if blockNum != "0x0" {
		t.Fatalf("block number mismatch: %s", blockNum)
	}
}

func TestJSONRPC_EthGetBalance(t *testing.T) {
	srv, cs := newTestServer(t)
	addr := "0x1111111111111111111111111111111111111111"
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 12.5, Nonce: 0}

	res := callJSONRPC(t, srv.URL, "eth_getBalance", []string{addr})
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	var balanceHex string
	if err := json.Unmarshal(res.Result, &balanceHex); err != nil {
		t.Fatalf("unmarshal balance: %v", err)
	}
	const expectedWei = "0xad78ebc5ac620000" // 12.5 CoX expressed in wei (1e18)
	if balanceHex != expectedWei {
		t.Fatalf("balance hex mismatch: %s", balanceHex)
	}
}

func TestJSONRPC_EthSendRawTransaction(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs.Accounts[addr] = &core.Account{Address: addr, Balance: 5, Nonce: 0}

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	rawBytes, _ := json.Marshal(tx)
	rawHex := "0x" + hex.EncodeToString(rawBytes)

	res := callJSONRPC(t, srv.URL, "eth_sendRawTransaction", []string{rawHex})
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	var txID string
	if err := json.Unmarshal(res.Result, &txID); err != nil {
		t.Fatalf("unmarshal tx id: %v", err)
	}
	if txID == "" {
		t.Fatalf("expected tx id in result")
	}
	cs.RLock()
	if len(cs.Mempool) != 1 {
		t.Fatalf("tx not added to mempool")
	}
	cs.RUnlock()
}
