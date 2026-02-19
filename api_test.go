package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*httptest.Server, *ChainState) {
	t.Helper()
	dir := t.TempDir()
	cs, err := LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	api := NewAPIServer(cs, dir, FounderAddress)
	srv := httptest.NewServer(api.Router())
	t.Cleanup(srv.Close)
	return srv, cs
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
	resp, err := http.Get(srv.URL + "/balance/CoXunknown123")
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
	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs.mu.Lock()
	cs.Accounts[addr] = &Account{Address: addr, Balance: 42.5, Nonce: 3}
	cs.mu.Unlock()

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
	var blocks []Block
	if err := json.NewDecoder(resp.Body).Decode(&blocks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(blocks) == 0 {
		t.Error("expected at least genesis block")
	}
}

// ---- /tx/submit -------------------------------------------------------------

func submitTx(t *testing.T, srvURL string, tx Transaction) (*http.Response, error) {
	t.Helper()
	body, err := json.Marshal(tx)
	if err != nil {
		return nil, err
	}
	return http.Post(srvURL+"/tx/submit", "application/json", bytes.NewReader(body))
}

func TestTxSubmit_ValidTx(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs.mu.Lock()
	cs.Accounts[addr] = &Account{Address: addr, Balance: 10.0, Nonce: 0}
	cs.mu.Unlock()

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

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

	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs.mu.Lock()
	cs.Accounts[addr] = &Account{Address: addr, Balance: 10.0, Nonce: 0}
	cs.mu.Unlock()

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
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

func TestTxSubmit_WrongFee(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs.mu.Lock()
	cs.Accounts[addr] = &Account{Address: addr, Balance: 10.0, Nonce: 0}
	cs.mu.Unlock()

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       0.1, // wrong
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

	resp, err := submitTx(t, srv.URL, tx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for wrong fee, got %d", resp.StatusCode)
	}
}

func TestTxSubmit_NonceMismatch(t *testing.T) {
	srv, cs := newTestServer(t)

	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs.mu.Lock()
	cs.Accounts[addr] = &Account{Address: addr, Balance: 10.0, Nonce: 0}
	cs.mu.Unlock()

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
		Nonce:     99, // should be 1
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

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
	tx := Transaction{
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
