package core_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "cocax-core/core"
)

const syncTimeoutDuration = 20 * time.Second

func TestStressP2PSync50Nodes(t *testing.T) {
	if os.Getenv("RUN_STRESS_TESTS") != "1" {
		t.Skip("set RUN_STRESS_TESTS=1 to run 50-node sync stress test")
	}

	seedAddr := freeAddr(t)
	seedDir := t.TempDir()
	seedState, err := core.LoadState(seedDir, "")
	if err != nil {
		t.Fatalf("LoadState seed: %v", err)
	}
	minerPriv, _ := core.GenerateKeyPair()
	minerAddr := core.DeriveAddress(&minerPriv.PublicKey)
	for i := 0; i < 3; i++ {
		if _, err := core.MineBlock(seedState, minerAddr, seedDir); err != nil {
			t.Fatalf("seed MineBlock(%d): %v", i, err)
		}
	}

	seed := core.NewP2PNode(seedAddr, nil, seedState, seedDir)
	seed.Start()

	const nodeCount = 50
	states := make([]*core.ChainState, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		dir := t.TempDir()
		addr := freeAddr(t)
		state, err := core.LoadState(dir, "")
		if err != nil {
			t.Fatalf("LoadState node %d: %v", i, err)
		}
		node := core.NewP2PNode(addr, []string{seedAddr}, state, dir)
		node.Start()
		states = append(states, state)
	}

	wantHeight := len(seedState.Chain)
	seedState.RLock()
	wantTipHash := seedState.Chain[wantHeight-1].Hash
	seedState.RUnlock()
	waitFor(t, time.Now().Add(syncTimeoutDuration), func() bool {
		for _, st := range states {
			st.RLock()
			ok := len(st.Chain) == wantHeight
			if ok {
				ok = st.Chain[len(st.Chain)-1].Hash == wantTipHash
			}
			st.RUnlock()
			if !ok {
				return false
			}
		}
		return true
	})
}

func TestStressIOWriteAtTenThousandBlocks(t *testing.T) {
	if os.Getenv("RUN_STRESS_TESTS") != "1" {
		t.Skip("set RUN_STRESS_TESTS=1 to run I/O stress test")
	}

	cs := &core.ChainState{
		Accounts: map[string]*core.Account{
			core.FounderAddress: {Address: core.FounderAddress, Balance: core.FounderAllocation, Nonce: 0},
		},
		MintedSupply: core.FounderAllocation,
	}

	prevHash := "genesis"
	cs.Chain = make([]core.Block, 0, 10000)
	for i := 0; i < 10000; i++ {
		hash := fmt.Sprintf("block-%d", i)
		cs.Chain = append(cs.Chain, core.Block{
			Index:      uint64(i),
			PrevHash:   prevHash,
			Hash:       hash,
			Timestamp:  time.Now().Unix(),
			Difficulty: core.InitialDifficulty,
			Nonce:      uint64(i),
			Miner:      core.FounderAddress,
		})
		prevHash = hash
	}

	dir := t.TempDir()
	start := time.Now()
	if err := core.SaveState(dir, cs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("saved 10,000 blocks to blocks.json/state.json in %s", elapsed)

	for _, name := range []string{"blocks.json", "state.json"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty after SaveState", name)
		}
	}
}
