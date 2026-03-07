package core_test

import (
	"crypto/ecdsa"
	"math"
	"os"
	"strings"
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

func TestMempool_OrderedInsertionAndLimit(t *testing.T) {
	origLimit := core.MempoolMaxSize
	core.MempoolMaxSize = 2
	defer func() { core.MempoolMaxSize = origLimit }()

	// Three funded senders so nonce rules remain satisfied while exercising ordering.
	privA, _ := core.GenerateKeyPair()
	addrA := core.DeriveAddress(&privA.PublicKey)
	privB, _ := core.GenerateKeyPair()
	addrB := core.DeriveAddress(&privB.PublicKey)
	privC, _ := core.GenerateKeyPair()
	addrC := core.DeriveAddress(&privC.PublicKey)

	cs := &core.ChainState{
		Accounts: map[string]*core.Account{
			addrA: {Address: addrA, Balance: 10, Nonce: 0},
			addrB: {Address: addrB, Balance: 10, Nonce: 0},
			addrC: {Address: addrC, Balance: 10, Nonce: 0},
		},
		Mempool: []core.Transaction{},
	}
	api := rpc.NewServer(cs, "", "")

	makeTx := func(priv *ecdsa.PrivateKey, ts int64) core.Transaction {
		tx := core.Transaction{
			From:      core.DeriveAddress(&priv.PublicKey),
			To:        "CoXrecipient000000000000000000000000000000000",
			Amount:    1.0,
			Fee:       core.FixedFee,
			Nonce:     1,
			Timestamp: ts,
		}
		_ = core.SignTransaction(&tx, priv)
		return tx
	}

	txSlow := makeTx(privA, time.Now().Add(2*time.Second).Unix())
	if err := api.ValidateAndAddTx(&txSlow); err != nil {
		t.Fatalf("add txSlow: %v", err)
	}
	txFast := makeTx(privB, time.Now().Add(1*time.Second).Unix())
	if err := api.ValidateAndAddTx(&txFast); err != nil {
		t.Fatalf("add txFast: %v", err)
	}

	cs.RLock()
	if len(cs.Mempool) != 2 {
		cs.RUnlock()
		t.Fatalf("expected mempool size 2, got %d", len(cs.Mempool))
	}
	if cs.Mempool[0].ID != txFast.ID || cs.Mempool[1].ID != txSlow.ID {
		cs.RUnlock()
		t.Fatalf("mempool not ordered by priority: got [%s, %s]", cs.Mempool[0].ID, cs.Mempool[1].ID)
	}
	cs.RUnlock()

	// Third tx should be rejected and not evict higher priority entries.
	txLowest := makeTx(privC, time.Now().Add(3*time.Second).Unix())
	err := api.ValidateAndAddTx(&txLowest)
	if err == nil || !strings.Contains(err.Error(), "mempool full") {
		t.Fatalf("expected mempool full error, got: %v", err)
	}
	cs.RLock()
	defer cs.RUnlock()
	if len(cs.Mempool) != 2 {
		t.Fatalf("expected mempool size to remain 2, got %d", len(cs.Mempool))
	}
	if cs.Mempool[0].ID != txFast.ID || cs.Mempool[1].ID != txSlow.ID {
		t.Fatalf("mempool contents changed after rejection: [%s, %s]", cs.Mempool[0].ID, cs.Mempool[1].ID)
	}
}

// cloneChainStateValues is a test helper that deep copies the chain state for
// isolated verification scenarios.
func cloneChainStateValues(src *core.ChainState) *core.ChainState {
	dst := &core.ChainState{
		Accounts:     make(map[string]*core.Account, len(src.Accounts)),
		Mempool:      append([]core.Transaction{}, src.Mempool...),
		Chain:        append([]core.Block{}, src.Chain...),
		MintedSupply: src.MintedSupply,
	}
	for addr, acc := range src.Accounts {
		if acc == nil {
			continue
		}
		c := *acc
		dst.Accounts[addr] = &c
	}
	return dst
}

func TestCloneChainStateValuesDeepCopy(t *testing.T) {
	cs := &core.ChainState{
		Accounts: map[string]*core.Account{
			"addr": {Address: "addr", Balance: 5, Nonce: 1},
		},
		Mempool:      []core.Transaction{{ID: "tx1"}},
		Chain:        []core.Block{{Index: 0, Hash: "hash0"}},
		MintedSupply: 10,
	}

	clone := cloneChainStateValues(cs)
	if clone.Accounts["addr"] == cs.Accounts["addr"] {
		t.Fatalf("accounts map was not deep copied")
	}
	clone.Accounts["addr"].Balance = 50
	if cs.Accounts["addr"].Balance == 50 {
		t.Fatalf("mutating clone account affected original")
	}
	if len(clone.Mempool) != len(cs.Mempool) || len(clone.Chain) != len(cs.Chain) {
		t.Fatalf("mempool or chain lengths differ after clone")
	}
	if clone.MintedSupply != cs.MintedSupply {
		t.Fatalf("minted supply not copied: got %.2f want %.2f", clone.MintedSupply, cs.MintedSupply)
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

func TestCreateBlockTemplateAssignsSequences(t *testing.T) {
	dir := t.TempDir()
	cs, _ := core.LoadState(dir, "")

	minerPriv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&minerPriv.PublicKey)

	senderPriv, _ := core.GenerateKeyPair()
	senderAddr := core.DeriveAddress(&senderPriv.PublicKey)
	cs.Accounts[senderAddr] = &core.Account{Address: senderAddr, Balance: 10, Nonce: 0}

	api := rpc.NewServer(cs, dir, minerAddr)
	tx1 := core.Transaction{
		From:      senderAddr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx1, senderPriv)
	if err := api.ValidateAndAddTx(&tx1); err != nil {
		t.Fatalf("ValidateAndAddTx tx1: %v", err)
	}

	cs.Accounts[senderAddr].Nonce = 1

	tx2 := core.Transaction{
		From:      senderAddr,
		To:        "CoXrecipient000000000000000000000000000000001",
		Amount:    1.5,
		Fee:       core.FixedFee,
		Nonce:     2,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx2, senderPriv)
	if err := api.ValidateAndAddTx(&tx2); err != nil {
		t.Fatalf("ValidateAndAddTx tx2: %v", err)
	}

	block, err := core.CreateBlockTemplate(cs, minerAddr)
	if err != nil {
		t.Fatalf("CreateBlockTemplate: %v", err)
	}

	if len(block.Transactions) != 3 {
		t.Fatalf("expected 3 transactions (coinbase + 2), got %d", len(block.Transactions))
	}
	if block.Transactions[0].SequenceNumber != 0 || !block.Transactions[0].IsCoinbase {
		t.Fatalf("expected coinbase at sequence 0, got seq=%d coinbase=%v", block.Transactions[0].SequenceNumber, block.Transactions[0].IsCoinbase)
	}
	if block.Transactions[1].SequenceNumber != 1 || block.Transactions[2].SequenceNumber != 2 {
		t.Fatalf("unexpected sequences: got %d and %d", block.Transactions[1].SequenceNumber, block.Transactions[2].SequenceNumber)
	}
}

func TestAddBlockParallelVerification(t *testing.T) {
	dir := t.TempDir()
	cs, _ := core.LoadState(dir, "")

	minerPriv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&minerPriv.PublicKey)

	senderPriv, _ := core.GenerateKeyPair()
	senderAddr := core.DeriveAddress(&senderPriv.PublicKey)
	cs.Accounts[senderAddr] = &core.Account{Address: senderAddr, Balance: 10, Nonce: 0}

	tx := core.Transaction{
		From:      senderAddr,
		To:        "CoXrecipient000000000000000000000000000000000",
		Amount:    1.0,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	_ = core.SignTransaction(&tx, senderPriv)
	api := rpc.NewServer(cs, dir, minerAddr)
	if err := api.ValidateAndAddTx(&tx); err != nil {
		t.Fatalf("ValidateAndAddTx: %v", err)
	}

	block, err := core.CreateBlockTemplate(cs, minerAddr)
	if err != nil {
		t.Fatalf("CreateBlockTemplate: %v", err)
	}

	clone := cloneChainStateValues(cs)

	peerVerifierA := func(b *core.Block) error { return core.VerifyBlock(cs, b) }
	peerVerifierB := func(b *core.Block) error { return core.VerifyBlock(clone, b) }
	if err := core.AddBlock(cs, block, dir, peerVerifierA, peerVerifierB); err != nil {
		t.Fatalf("AddBlock with parallel verifiers: %v", err)
	}

	cs.RLock()
	defer cs.RUnlock()
	if len(cs.Chain) < 2 {
		t.Fatalf("expected new block to be added, chain length %d", len(cs.Chain))
	}
	if len(cs.Mempool) != 0 {
		t.Fatalf("expected mempool to be cleared, size %d", len(cs.Mempool))
	}
}

func TestAddBlockRejectsDuplicateNonceAndPenalises(t *testing.T) {
	dir := t.TempDir()
	cs, _ := core.LoadState(dir, "")

	minerPriv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&minerPriv.PublicKey)
	cs.Accounts[minerAddr] = &core.Account{Address: minerAddr, Balance: 5, Nonce: 0}

	senderPriv, _ := core.GenerateKeyPair()
	senderAddr := core.DeriveAddress(&senderPriv.PublicKey)
	cs.Accounts[senderAddr] = &core.Account{Address: senderAddr, Balance: 5, Nonce: 0}

	block, err := core.CreateBlockTemplate(cs, minerAddr)
	if err != nil {
		t.Fatalf("CreateBlockTemplate: %v", err)
	}

	tx1 := core.Transaction{
		From:           senderAddr,
		To:             "CoXdouble0000000000000000000000000000000000",
		Amount:         1.0,
		Fee:            core.FixedFee,
		Nonce:          1,
		Timestamp:      time.Now().Unix(),
		SequenceNumber: 1,
	}
	_ = core.SignTransaction(&tx1, senderPriv)
	tx1.ID = core.TxID(&tx1)

	tx2 := core.Transaction{
		From:           tx1.From,
		To:             tx1.To,
		Amount:         tx1.Amount,
		Fee:            tx1.Fee,
		Nonce:          tx1.Nonce,
		Timestamp:      tx1.Timestamp + 1,
		SequenceNumber: 2,
	}
	_ = core.SignTransaction(&tx2, senderPriv)
	tx2.ID = core.TxID(&tx2)

	block.Transactions = append(block.Transactions, tx1, tx2)
	block.Hash = core.BlockHash(block)

	startBalance := cs.Accounts[minerAddr].Balance
	err = core.AddBlock(cs, block, dir)
	if err == nil {
		t.Fatal("expected block to be rejected due to double spend")
	}
	if !strings.Contains(err.Error(), "nonce mismatch") {
		t.Fatalf("expected nonce mismatch rejection, got: %v", err)
	}

	cs.RLock()
	defer cs.RUnlock()
	if cs.Accounts[minerAddr].Balance >= startBalance {
		t.Errorf("expected miner penalty to reduce balance, balance %.4f", cs.Accounts[minerAddr].Balance)
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
