package main

import (
	"math"
	"os"
	"testing"
	"time"
)

// ---- Halving schedule -------------------------------------------------------

func TestBlockReward_Genesis(t *testing.T) {
	// Block 0 is genesis (no reward); block 1 should return BaseBlockReward.
	r := BlockReward(1)
	if !floatEqual(r, BaseBlockReward) {
		t.Errorf("expected %.4f, got %.4f", BaseBlockReward, r)
	}
}

func TestBlockReward_Halving(t *testing.T) {
	tests := []struct {
		blockIndex uint64
		want       float64
	}{
		{0, BaseBlockReward},
		{999_999, BaseBlockReward},
		{1_000_000, BaseBlockReward / 2},
		{1_999_999, BaseBlockReward / 2},
		{2_000_000, BaseBlockReward / 4},
		{3_000_000, BaseBlockReward / 8},
		{10_000_000, BaseBlockReward / math.Pow(2, 10)},
	}
	for _, tt := range tests {
		got := BlockReward(tt.blockIndex)
		if !floatEqual(got, tt.want) {
			t.Errorf("BlockReward(%d): want %.10f, got %.10f", tt.blockIndex, tt.want, got)
		}
	}
}

// ---- Address derivation -----------------------------------------------------

func TestDeriveAddress_Consistent(t *testing.T) {
	priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	addr1 := DeriveAddress(&priv.PublicKey)
	if len(addr1) == 0 {
		t.Fatal("empty address")
	}
	// Must start with "CoX".
	if addr1[:3] != AddressPrefix {
		t.Errorf("address does not start with %q: %s", AddressPrefix, addr1)
	}
	// Derivation must be deterministic.
	addr2 := DeriveAddress(&priv.PublicKey)
	if addr1 != addr2 {
		t.Errorf("address derivation is not deterministic: %s vs %s", addr1, addr2)
	}
}

func TestDeriveAddress_DifferentKeys(t *testing.T) {
	k1, _ := GenerateKeyPair()
	k2, _ := GenerateKeyPair()
	if DeriveAddress(&k1.PublicKey) == DeriveAddress(&k2.PublicKey) {
		t.Error("two different keys produced the same address")
	}
}

// ---- Transaction signing / verification -------------------------------------

func newSignedTx(t *testing.T) (Transaction, func()) {
	t.Helper()
	priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	addr := DeriveAddress(&priv.PublicKey)

	// Fund the sender so balance checks pass when tested through the mempool.
	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	if err := SignTransaction(&tx, priv); err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	tx.ID = TxID(&tx)
	return tx, func() {}
}

func TestSignVerifyTransaction_Valid(t *testing.T) {
	tx, _ := newSignedTx(t)
	if err := VerifyTransaction(&tx); err != nil {
		t.Errorf("expected valid tx to pass verification: %v", err)
	}
}

func TestVerifyTransaction_MissingSignature(t *testing.T) {
	tx, _ := newSignedTx(t)
	tx.SigR = ""
	tx.SigS = ""
	if err := VerifyTransaction(&tx); err == nil {
		t.Error("expected error for missing signature, got nil")
	}
}

func TestVerifyTransaction_WrongAddress(t *testing.T) {
	tx, _ := newSignedTx(t)
	// Tamper with From address so it no longer matches derived key.
	tx.From = "CoXdeadbeefdeadbeef0000000000000000000000000"
	if err := VerifyTransaction(&tx); err == nil {
		t.Error("expected error for address mismatch, got nil")
	}
}

func TestVerifyTransaction_TamperedPayload(t *testing.T) {
	tx, _ := newSignedTx(t)
	// Change amount after signing – signature should now be invalid.
	tx.Amount = 999.0
	if err := VerifyTransaction(&tx); err == nil {
		t.Error("expected error after tampering payload, got nil")
	}
}

// ---- Mempool validation (via APIServer) -------------------------------------

func newFundedState(addr string, balance float64) *ChainState {
	cs := &ChainState{
		Accounts: map[string]*Account{
			addr: {Address: addr, Balance: balance, Nonce: 0},
		},
		Mempool: []Transaction{},
	}
	return cs
}

func TestMempool_ValidTxAccepted(t *testing.T) {
	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 10.0)
	api := NewAPIServer(cs, "", "")

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

	if err := api.validateAndAddTx(&tx); err != nil {
		t.Errorf("expected valid tx to be accepted: %v", err)
	}
	cs.mu.Lock()
	if len(cs.Mempool) != 1 {
		t.Errorf("expected mempool size 1, got %d", len(cs.Mempool))
	}
	cs.mu.Unlock()
}

func TestMempool_NonceMismatch(t *testing.T) {
	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 10.0)
	api := NewAPIServer(cs, "", "")

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
		Nonce:     5, // should be 1
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

	if err := api.validateAndAddTx(&tx); err == nil {
		t.Error("expected nonce mismatch error, got nil")
	}
}

func TestMempool_WrongFeeRejected(t *testing.T) {
	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 10.0)
	api := NewAPIServer(cs, "", "")

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       0.05, // wrong fee
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

	if err := api.validateAndAddTx(&tx); err == nil {
		t.Error("expected fee enforcement error, got nil")
	}
}

func TestMempool_InsufficientBalance(t *testing.T) {
	priv, _ := GenerateKeyPair()
	addr := DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 0.005) // way too little
	api := NewAPIServer(cs, "", "")

	tx := Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = SignTransaction(&tx, priv)

	if err := api.validateAndAddTx(&tx); err == nil {
		t.Error("expected insufficient balance error, got nil")
	}
}

// ---- Genesis / founder allocation -------------------------------------------

func TestFounderAllocationAtGenesis(t *testing.T) {
	dir := t.TempDir()
	cs, err := LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	acc, ok := cs.Accounts[FounderAddress]
	if !ok {
		t.Fatal("founder account not found after genesis")
	}
	if !floatEqual(acc.Balance, FounderAllocation) {
		t.Errorf("founder balance: want %.2f, got %.2f", FounderAllocation, acc.Balance)
	}
	if len(cs.Chain) != 1 {
		t.Errorf("expected 1 genesis block, got %d", len(cs.Chain))
	}
}

func TestLoadState_Persists(t *testing.T) {
	dir := t.TempDir()
	cs, err := LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState (first): %v", err)
	}
	// Save explicitly then reload.
	if err := SaveState(dir, cs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	cs2, err := LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState (second): %v", err)
	}
	if len(cs2.Chain) != len(cs.Chain) {
		t.Errorf("chain length mismatch after reload: %d vs %d", len(cs2.Chain), len(cs.Chain))
	}
	if !floatEqual(cs2.MintedSupply, cs.MintedSupply) {
		t.Errorf("minted supply mismatch after reload: %.2f vs %.2f", cs2.MintedSupply, cs.MintedSupply)
	}
}

// ---- Mine block -------------------------------------------------------------

func TestMineBlock(t *testing.T) {
	dir := t.TempDir()
	cs, _ := LoadState(dir, "")
	// Use a fresh key as miner address.
	priv, _ := GenerateKeyPair()
	minerAddr := DeriveAddress(&priv.PublicKey)

	block, err := MineBlock(cs, minerAddr, dir)
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}
	if block.Index != 1 {
		t.Errorf("expected block index 1, got %d", block.Index)
	}
	if block.Miner != minerAddr {
		t.Errorf("block miner mismatch")
	}

	cs.mu.RLock()
	minerAcc := cs.Accounts[minerAddr]
	cs.mu.RUnlock()

	expectedReward := BlockReward(1)
	if !floatEqual(minerAcc.Balance, expectedReward) {
		t.Errorf("miner balance: want %.8f, got %.8f", expectedReward, minerAcc.Balance)
	}
}

// ---- Persistence files exist ------------------------------------------------

func TestSaveState_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	cs, _ := LoadState(dir, "")
	// Files should already exist after LoadState (genesis path).
	if _, err := os.Stat(dir + "/blocks.json"); err != nil {
		t.Errorf("blocks.json not created: %v", err)
	}
	if _, err := os.Stat(dir + "/state.json"); err != nil {
		t.Errorf("state.json not created: %v", err)
	}
	_ = cs
}

// ---- Custom founder address (-founder flag) ---------------------------------

func TestCustomFounderAddress(t *testing.T) {
	// Generate a fresh address to use as a custom founder.
	priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	customFounder := DeriveAddress(&priv.PublicKey)

	dir := t.TempDir()
	cs, err := LoadState(dir, customFounder)
	if err != nil {
		t.Fatalf("LoadState with custom founder: %v", err)
	}

	// Custom founder should have the allocation.
	acc, ok := cs.Accounts[customFounder]
	if !ok {
		t.Fatal("custom founder account not found after genesis")
	}
	if !floatEqual(acc.Balance, FounderAllocation) {
		t.Errorf("custom founder balance: want %.2f, got %.2f", FounderAllocation, acc.Balance)
	}
	// The default placeholder must NOT have received the allocation.
	if _, ok := cs.Accounts[FounderAddress]; ok {
		t.Error("default founder placeholder should not have an account when custom founder is set")
	}
}

// ---- -genaddr logic (key generation + address derivation) -------------------

func TestGenerateKeyPair_ProducesValidAddress(t *testing.T) {
	priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	addr := DeriveAddress(&priv.PublicKey)
	if len(addr) < len(AddressPrefix)+1 {
		t.Fatalf("address too short: %s", addr)
	}
	if addr[:len(AddressPrefix)] != AddressPrefix {
		t.Errorf("address missing prefix %q: %s", AddressPrefix, addr)
	}
}
