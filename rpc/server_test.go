package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cocax-core/core"
)

func TestHandleBalanceAliases(t *testing.T) {
	accountAddr := "CoX1111111111111111111111111111111111111111"
	state := &core.ChainState{
		Accounts: map[string]*core.Account{
			accountAddr: {Address: accountAddr, Balance: 5.5, Nonce: 2},
		},
	}
	api := NewServer(state, "", "")

	for _, path := range []string{"/api/balance/" + accountAddr, "/balance?address=" + accountAddr} {
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
		if resp["address"] != accountAddr {
			t.Fatalf("path %s: address mismatch, got %v", path, resp["address"])
		}
		if resp["balance"] != state.Accounts[accountAddr].Balance {
			t.Fatalf("path %s: balance mismatch, got %v", path, resp["balance"])
		}
		nonceVal, ok := resp["nonce"].(float64)
		if !ok {
			t.Fatalf("path %s: nonce not present or wrong type", path)
		}
		if uint64(nonceVal) != state.Accounts[accountAddr].Nonce {
			t.Fatalf("path %s: nonce mismatch, got %v", path, nonceVal)
		}
	}
}

func TestHandleTransactionsIncludesPendingAndConfirmed(t *testing.T) {
	walletAddr := "CoX2222222222222222222222222222222222222222"
	counterpartyAddr := "CoX3333333333333333333333333333333333333333"
	confirmedTx := core.Transaction{ID: "tx1", From: walletAddr, To: counterpartyAddr, Amount: 1}
	pendingTx := core.Transaction{ID: "tx2", From: counterpartyAddr, To: walletAddr, Amount: 2}

	state := &core.ChainState{
		Chain: []core.Block{{Index: 1, Hash: "hash1", Transactions: []core.Transaction{confirmedTx}}},
		Mempool: []core.Transaction{
			pendingTx,
		},
		Accounts: map[string]*core.Account{},
	}
	api := NewServer(state, "", "")

	req := httptest.NewRequest(http.MethodGet, "/transactions/"+walletAddr, nil)
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
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Confirmed) != 1 || len(resp.Pending) != 1 {
		t.Fatalf("expected 1 confirmed and 1 pending, got %d and %d", len(resp.Confirmed), len(resp.Pending))
	}
	if resp.Confirmed[0].ID != confirmedTx.ID {
		t.Fatalf("confirmed tx id mismatch: %+v", resp.Confirmed[0])
	}
	if resp.Confirmed[0].BlockIndex != 1 {
		t.Fatalf("confirmed tx block index mismatch: %+v", resp.Confirmed[0])
	}
	if resp.Confirmed[0].BlockHash != "hash1" {
		t.Fatalf("confirmed tx block hash mismatch: %+v", resp.Confirmed[0])
	}
	if resp.Confirmed[0].Status != "confirmed" {
		t.Fatalf("confirmed tx status mismatch: %+v", resp.Confirmed[0])
	}
	if resp.Pending[0].ID != pendingTx.ID {
		t.Fatalf("pending tx id mismatch: %+v", resp.Pending[0])
	}
	if resp.Pending[0].Status != "pending" {
		t.Fatalf("pending tx status mismatch: %+v", resp.Pending[0])
	}
	if resp.Pending[0].Amount != pendingTx.Amount {
		t.Fatalf("pending tx amount mismatch: %+v", resp.Pending[0])
	}
}
