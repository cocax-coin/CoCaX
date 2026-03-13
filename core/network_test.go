package core_test

import (
	"net"
	"testing"
	"time"

	core "cocax-core/core"
	"cocax-core/rpc"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func waitFor(t *testing.T, deadline time.Time, fn func() bool) {
	t.Helper()
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition not met before timeout")
}

func TestP2PTxBroadcastAddsToPeerMempool(t *testing.T) {
	addr1 := freeAddr(t)
	addr2 := freeAddr(t)

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	cs1, _ := core.LoadState(dir1, "")
	cs2, _ := core.LoadState(dir2, "")

	senderPriv, _ := core.GenerateKeyPair()
	senderAddr := core.DeriveAddress(&senderPriv.PublicKey)
	cs1.Accounts[senderAddr] = &core.Account{Address: senderAddr, Balance: 10, Nonce: 0}
	cs2.Accounts[senderAddr] = &core.Account{Address: senderAddr, Balance: 10, Nonce: 0}

	p2pPeer := core.NewP2PNode(addr2, nil, cs2, dir2)
	p2pPeer.Start()
	p2pOrigin := core.NewP2PNode(addr1, []string{addr2}, cs1, dir1)
	p2pOrigin.Start()

	api := rpc.NewServer(cs1, dir1, core.FounderAddress)
	api.SetP2PNode(p2pOrigin)

	tx := core.Transaction{
		From:      senderAddr,
		To:        "CoXreceiver000000000000000000000000000000000",
		Amount:    1,
		Fee:       core.FixedFee,
		Nonce:     1,
		Timestamp: time.Now().Unix(),
	}
	if err := core.SignTransaction(&tx, senderPriv); err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}

	if err := api.ValidateAndAddTx(&tx); err != nil {
		t.Fatalf("ValidateAndAddTx: %v", err)
	}

	waitFor(t, time.Now().Add(3*time.Second), func() bool {
		cs2.RLock()
		defer cs2.RUnlock()
		return len(cs2.Mempool) == 1 && cs2.Mempool[0].ID == tx.ID
	})
}

func TestP2PBlockBroadcastAppendsToPeerChain(t *testing.T) {
	addr1 := freeAddr(t)
	addr2 := freeAddr(t)

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	cs1, _ := core.LoadState(dir1, "")
	cs2, _ := core.LoadState(dir2, "")

	minerPriv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&minerPriv.PublicKey)

	p2pPeer := core.NewP2PNode(addr2, nil, cs2, dir2)
	p2pPeer.Start()
	p2pOrigin := core.NewP2PNode(addr1, []string{addr2}, cs1, dir1)
	p2pOrigin.Start()

	block, err := core.MineBlock(cs1, minerAddr, dir1)
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}

	p2pOrigin.BroadcastBlock(block)

	waitFor(t, time.Now().Add(3*time.Second), func() bool {
		cs2.RLock()
		defer cs2.RUnlock()
		return len(cs2.Chain) >= 2 && cs2.Chain[len(cs2.Chain)-1].Hash == block.Hash
	})
}

func TestP2PSyncFetchesMissingBlocks(t *testing.T) {
	addrLong := freeAddr(t)
	addrShort := freeAddr(t)

	dirLong := t.TempDir()
	dirShort := t.TempDir()

	csLong, err := core.LoadState(dirLong, "")
	if err != nil {
		t.Fatalf("LoadState long: %v", err)
	}
	csShort, err := core.LoadState(dirShort, "")
	if err != nil {
		t.Fatalf("LoadState short: %v", err)
	}

	minerPriv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&minerPriv.PublicKey)

	if _, err := core.MineBlock(csLong, minerAddr, dirLong); err != nil {
		t.Fatalf("MineBlock (1): %v", err)
	}
	if _, err := core.MineBlock(csLong, minerAddr, dirLong); err != nil {
		t.Fatalf("MineBlock (2): %v", err)
	}

	p2pLong := core.NewP2PNode(addrLong, nil, csLong, dirLong)
	p2pLong.Start()
	p2pShort := core.NewP2PNode(addrShort, []string{addrLong}, csShort, dirShort)
	p2pShort.Start()

	wantHeight := len(csLong.Chain)
	waitFor(t, time.Now().Add(4*time.Second), func() bool {
		csShort.RLock()
		defer csShort.RUnlock()
		return len(csShort.Chain) == wantHeight
	})
}
