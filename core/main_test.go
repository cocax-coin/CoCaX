package core_test

import (
	"math"
	"os"
	"testing"
	"time"

	core "cocax-core/core"
	"cocax-core/rpc"
)

// ---- Halving schedule -------------------------------------------------------

func TestBlockReward_Genesis(t *testing.T) {
	// Block 0 is genesis (no reward); block 1 should return BaseBlockReward.
	r := core.BlockReward(1)
	if !core.FloatEqual(r, core.BaseBlockReward) {
		t.Errorf("expected %.4f, got %.4f", core.BaseBlockReward, r)
	}
}

func TestBlockReward_Halving(t *testing.T) {
	tests := []struct {
		blockIndex uint64
		want       float64
	}{
		{0, core.BaseBlockReward},
		{999_999, core.BaseBlockReward},
		{1_000_000, core.BaseBlockReward / 2},
		{1_999_999, core.BaseBlockReward / 2},
		{2_000_000, core.BaseBlockReward / 4},
		{3_000_000, core.BaseBlockReward / 8},
		{10_000_000, core.BaseBlockReward / math.Pow(2, 10)},
	}
	for _, tt := range tests {
		got := core.BlockReward(tt.blockIndex)
		if !core.FloatEqual(got, tt.want) {
			t.Errorf("BlockReward(%d): want %.10f, got %.10f", tt.blockIndex, tt.want, got)
		}
	}
}

// ---- Address derivation -----------------------------------------------------

func TestDeriveAddress_Consistent(t *testing.T) {
	priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	addr1 := core.DeriveAddress(&priv.PublicKey)
	if len(addr1) == 0 {
		t.Fatal("empty address")
	}
	// Must start with "CoX".
	if addr1[:3] != core.AddressPrefix {
		t.Errorf("address does not start with %q: %s", core.AddressPrefix, addr1)
	}
	// Derivation must be deterministic.
	addr2 := core.DeriveAddress(&priv.PublicKey)
	if addr1 != addr2 {
		t.Errorf("address derivation is not deterministic: %s vs %s", addr1, addr2)
	}
}

func TestDeriveAddress_DifferentKeys(t *testing.T) {
	k1, _ := core.GenerateKeyPair()
	k2, _ := core.GenerateKeyPair()
	if core.DeriveAddress(&k1.PublicKey) == core.DeriveAddress(&k2.PublicKey) {
		t.Error("two different keys produced the same address")
	}
}

// ---- Transaction signing / verification -------------------------------------

func newSignedTx(t *testing.T) (core.Transaction, func()) {
	t.Helper()
	priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	addr := core.DeriveAddress(&priv.PublicKey)

	// Fund the sender so balance checks pass when tested through the mempool.
	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	if err := core.SignTransaction(&tx, priv); err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	tx.ID = core.TxID(&tx)
	return tx, func() {}
}

func TestSignVerifyTransaction_Valid(t *testing.T) {
	tx, _ := newSignedTx(t)
	if err := core.VerifyTransaction(&tx); err != nil {
		t.Errorf("expected valid tx to pass verification: %v", err)
	}
}

func TestVerifyTransaction_MissingSignature(t *testing.T) {
	tx, _ := newSignedTx(t)
	tx.SigR = ""
	tx.SigS = ""
	if err := core.VerifyTransaction(&tx); err == nil {
		t.Error("expected error for missing signature, got nil")
	}
}

func TestVerifyTransaction_WrongAddress(t *testing.T) {
	tx, _ := newSignedTx(t)
	// Tamper with From address so it no longer matches derived key.
	tx.From = "CoXdeadbeefdeadbeef0000000000000000000000000"
	if err := core.VerifyTransaction(&tx); err == nil {
		t.Error("expected error for address mismatch, got nil")
	}
}

func TestVerifyTransaction_TamperedPayload(t *testing.T) {
	tx, _ := newSignedTx(t)
	// Change amount after signing – signature should now be invalid.
	tx.Amount = 999.0
	if err := core.VerifyTransaction(&tx); err == nil {
		t.Error("expected error after tampering payload, got nil")
	}
}

// ---- Mempool validation (via APIServer) -------------------------------------

func newFundedState(addr string, balance float64) *core.ChainState {
	cs := &core.ChainState{
		Accounts: map[string]*core.Account{
			addr: {Address: addr, Balance: balance, Nonce: 0},
		},
		Mempool: []core.Transaction{},
	}
	return cs
}

func TestMempool_ValidTxAccepted(t *testing.T) {
	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 10.0)
	api := rpc.NewServer(cs, "", "")

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	if err := api.ValidateAndAddTx(&tx); err != nil {
		t.Errorf("expected valid tx to be accepted: %v", err)
	}
	cs.Lock()
	if len(cs.Mempool) != 1 {
		t.Errorf("expected mempool size 1, got %d", len(cs.Mempool))
	}
	cs.Unlock()
}

func TestMempool_NonceMismatch(t *testing.T) {
	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 10.0)
	api := rpc.NewServer(cs, "", "")

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     5, // should be 1
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	if err := api.ValidateAndAddTx(&tx); err == nil {
		t.Error("expected nonce mismatch error, got nil")
	}
}

func TestMempool_WrongFeeRejected(t *testing.T) {
	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 10.0)
	api := rpc.NewServer(cs, "", "")

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       0.05, // wrong fee
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	if err := api.ValidateAndAddTx(&tx); err == nil {
		t.Error("expected fee enforcement error, got nil")
	}
}

func TestMempool_InsufficientBalance(t *testing.T) {
	priv, _ := core.GenerateKeyPair()
	addr := core.DeriveAddress(&priv.PublicKey)
	cs := newFundedState(addr, 0.005) // way too little
	api := rpc.NewServer(cs, "", "")

	tx := core.Transaction{
		From:      addr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, priv)

	if err := api.ValidateAndAddTx(&tx); err == nil {
		t.Error("expected insufficient balance error, got nil")
	}
}

// ---- Genesis / founder allocation -------------------------------------------

func TestFounderAllocationAtGenesis(t *testing.T) {
	dir := t.TempDir()
	cs, err := core.LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	acc, ok := cs.Accounts[core.FounderAddress]
	if !ok {
		t.Fatal("founder account not found after genesis")
	}
	if !core.FloatEqual(acc.Balance, core.FounderAllocation) {
		t.Errorf("founder balance: want %.2f, got %.2f", core.FounderAllocation, acc.Balance)
	}
	if len(cs.Chain) != 1 {
		t.Errorf("expected 1 genesis block, got %d", len(cs.Chain))
	}
}

func TestLoadState_Persists(t *testing.T) {
	dir := t.TempDir()
	cs, err := core.LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState (first): %v", err)
	}
	// Save explicitly then reload.
	if err := core.SaveState(dir, cs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	cs2, err := core.LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState (second): %v", err)
	}
	if len(cs2.Chain) != len(cs.Chain) {
		t.Errorf("chain length mismatch after reload: %d vs %d", len(cs2.Chain), len(cs.Chain))
	}
	if !core.FloatEqual(cs2.MintedSupply, cs.MintedSupply) {
		t.Errorf("minted supply mismatch after reload: %.2f vs %.2f", cs2.MintedSupply, cs.MintedSupply)
	}
}

// ---- Mine block -------------------------------------------------------------

func TestMineBlock(t *testing.T) {
	dir := t.TempDir()
	cs, _ := core.LoadState(dir, "")
	// Use a fresh key as miner address.
	priv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&priv.PublicKey)

	block, err := core.MineBlock(cs, minerAddr, dir)
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}
	if block.Index != 1 {
		t.Errorf("expected block index 1, got %d", block.Index)
	}
	if block.Miner != minerAddr {
		t.Errorf("block miner mismatch")
	}

	cs.RLock()
	minerAcc := cs.Accounts[minerAddr]
	cs.RUnlock()

	expectedReward := core.BlockReward(1)
	if !core.FloatEqual(minerAcc.Balance, expectedReward) {
		t.Errorf("miner balance: want %.8f, got %.8f", expectedReward, minerAcc.Balance)
	}
}

// ---- Persistence files exist ------------------------------------------------

func TestSaveState_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	cs, _ := core.LoadState(dir, "")
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
	priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	customFounder := core.DeriveAddress(&priv.PublicKey)

	dir := t.TempDir()
	cs, err := core.LoadState(dir, customFounder)
	if err != nil {
		t.Fatalf("LoadState with custom founder: %v", err)
	}

	// Custom founder should have the allocation.
	acc, ok := cs.Accounts[customFounder]
	if !ok {
		t.Fatal("custom founder account not found after genesis")
	}
	if !core.FloatEqual(acc.Balance, core.FounderAllocation) {
		t.Errorf("custom founder balance: want %.2f, got %.2f", core.FounderAllocation, acc.Balance)
	}
	// The default placeholder must NOT have received the allocation.
	if _, ok := cs.Accounts[core.FounderAddress]; ok {
		t.Error("default founder placeholder should not have an account when custom founder is set")
	}
}

// ---- -genaddr logic (key generation + address derivation) -------------------

func TestGenerateKeyPair_ProducesValidAddress(t *testing.T) {
	priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	addr := core.DeriveAddress(&priv.PublicKey)
	if len(addr) < len(core.AddressPrefix)+1 {
		t.Fatalf("address too short: %s", addr)
	}
	if addr[:len(core.AddressPrefix)] != core.AddressPrefix {
		t.Errorf("address missing prefix %q: %s", core.AddressPrefix, addr)
	}
}
