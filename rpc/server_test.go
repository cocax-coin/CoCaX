package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cocax-core/core"
)

func TestHandleBalanceAliases(t *testing.T) {
	addr := "CoX1111111111111111111111111111111111111111"
	state := &core.ChainState{
		Accounts: map[string]*core.Account{
			addr: {Address: addr, Balance: 5.5, Nonce: 2},
		},
	}
	api := NewServer(state, "", "")

	for _, path := range []string{"/api/balance/" + addr, "/balance?address=" + addr} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		api.Router().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("path %s: expected status 200, got %d", path, rr.Code)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("path %s: failed to decode response: %v", path, err)
		}
		if resp["address"] != addr {
			t.Fatalf("path %s: address mismatch, got %v", path, resp["address"])
		}
		if resp["balance"] != state.Accounts[addr].Balance {
			t.Fatalf("path %s: balance mismatch, got %v", path, resp["balance"])
		}
		nonceVal, ok := resp["nonce"].(float64)
		if !ok {
			t.Fatalf("path %s: nonce not present or wrong type", path)
		}
		if uint64(nonceVal) != state.Accounts[addr].Nonce {
			t.Fatalf("path %s: nonce mismatch, got %v", path, nonceVal)
		}
	}
}

func TestHandleTransactionsIncludesPendingAndConfirmed(t *testing.T) {
	addr := "CoX2222222222222222222222222222222222222222"
	other := "CoX3333333333333333333333333333333333333333"
	confirmedTx := core.Transaction{ID: "tx1", From: addr, To: other, Amount: 1}
	pendingTx := core.Transaction{ID: "tx2", From: other, To: addr, Amount: 2}

	state := &core.ChainState{
		Chain: []core.Block{{Index: 1, Hash: "hash1", Transactions: []core.Transaction{confirmedTx}}},
		Mempool: []core.Transaction{
			pendingTx,
		},
		Accounts: map[string]*core.Account{},
	}
	api := NewServer(state, "", "")

	req := httptest.NewRequest(http.MethodGet, "/transactions/"+addr, nil)
	rr := httptest.NewRecorder()
	api.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var resp struct {
		Confirmed []struct {
			ID         string  `json:"id"`
			Amount     float64 `json:"amount"`
			Status     string  `json:"status"`
			BlockIndex uint64  `json:"block_index"`
			BlockHash  string  `json:"block_hash"`
		} `json:"confirmed"`
		Pending []struct {
			ID     string  `json:"id"`
			Amount float64 `json:"amount"`
			Status string  `json:"status"`
		} `json:"pending"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Confirmed) != 1 || len(resp.Pending) != 1 {
		t.Fatalf("expected 1 confirmed and 1 pending, got %d and %d", len(resp.Confirmed), len(resp.Pending))
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %v", resp.Total)
	}
	if resp.Confirmed[0].ID != confirmedTx.ID || resp.Confirmed[0].BlockIndex != 1 || resp.Confirmed[0].BlockHash != "hash1" || resp.Confirmed[0].Status != "confirmed" {
		t.Fatalf("confirmed tx metadata mismatch: %+v", resp.Confirmed[0])
	}
	if resp.Pending[0].ID != pendingTx.ID || resp.Pending[0].Status != "pending" || resp.Pending[0].Amount != pendingTx.Amount {
		t.Fatalf("pending tx mismatch: %+v", resp.Pending[0])
	}
}
