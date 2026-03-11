package core_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	core "cocax-core/core"
)

func decodeBlockHash(t *testing.T, blk *core.Block) []byte {
	t.Helper()
	hashBytes, err := hex.DecodeString(blk.Hash)
	if err != nil {
		t.Fatalf("decode hash: %v", err)
	}
	return hashBytes
}

func newHashedBlock(t *testing.T) *core.Block {
	t.Helper()
	blk := &core.Block{
		Index:     1,
		PrevHash:  strings.Repeat("0", 64),
		Timestamp: time.Now().Unix(),
		Miner:     "validator-miner",
	}
	blk.Hash = core.BlockHash(blk)
	return blk
}

func TestAppendVerificationSequence(t *testing.T) {
	blk := newHashedBlock(t)
	hashBytes := decodeBlockHash(t, blk)

	priv1, _ := core.GenerateKeyPair()
	sig1, err := ecdsa.SignASN1(rand.Reader, priv1, hashBytes)
	if err != nil {
		t.Fatalf("sign1: %v", err)
	}
	v1 := core.Verification{VerifierID: "validator-1", Signature: sig1, Index: 0}
	if err := blk.AppendVerification(v1, &priv1.PublicKey); err != nil {
		t.Fatalf("append v1: %v", err)
	}

	priv2, _ := core.GenerateKeyPair()
	sig2, err := ecdsa.SignASN1(rand.Reader, priv2, hashBytes)
	if err != nil {
		t.Fatalf("sign2: %v", err)
	}
	v2 := core.Verification{VerifierID: "validator-2", Signature: sig2, Index: 1}
	if err := blk.AppendVerification(v2, &priv2.PublicKey); err != nil {
		t.Fatalf("append v2: %v", err)
	}

	if len(blk.VerifiedSequence) != 2 {
		t.Fatalf("expected 2 verifications, got %d", len(blk.VerifiedSequence))
	}
	if blk.VerifiedSequence[1].Index != 1 {
		t.Fatalf("unexpected index on second verification: %d", blk.VerifiedSequence[1].Index)
	}
}

func TestAppendVerificationValidation(t *testing.T) {
	blk := newHashedBlock(t)
	hashBytes := decodeBlockHash(t, blk)

	priv, _ := core.GenerateKeyPair()
	validSig, _ := ecdsa.SignASN1(rand.Reader, priv, hashBytes)
	if err := blk.AppendVerification(core.Verification{VerifierID: "validator-1", Signature: validSig, Index: 0}, &priv.PublicKey); err != nil {
		t.Fatalf("append first: %v", err)
	}

	dupSig, _ := ecdsa.SignASN1(rand.Reader, priv, hashBytes)
	if err := blk.AppendVerification(core.Verification{VerifierID: "validator-1", Signature: dupSig, Index: 1}, &priv.PublicKey); err == nil {
		t.Fatalf("expected duplicate verifier rejection")
	}

	priv2, _ := core.GenerateKeyPair()
	wrongIndexSig, _ := ecdsa.SignASN1(rand.Reader, priv2, hashBytes)
	if err := blk.AppendVerification(core.Verification{VerifierID: "validator-2", Signature: wrongIndexSig, Index: 3}, &priv2.PublicKey); err == nil {
		t.Fatalf("expected index mismatch rejection")
	}

	badSig, _ := ecdsa.SignASN1(rand.Reader, priv2, hashBytes)
	if len(badSig) == 0 {
		t.Fatalf("expected non-empty signature for tampering")
	}
	badSig[0] ^= 0xFF
	err := blk.AppendVerification(core.Verification{VerifierID: "validator-2", Signature: badSig, Index: 1}, &priv2.PublicKey)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature verification failure, got: %v", err)
	}
}

func TestVerifiedSequencePersists(t *testing.T) {
	dir := t.TempDir()
	cs, err := core.LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	last := cs.Chain[len(cs.Chain)-1]
	blk := core.Block{
		Index:     last.Index + 1,
		PrevHash:  last.Hash,
		Timestamp: time.Now().Unix(),
		Miner:     "miner-persist",
		Reward:    0,
		VerifiedSequence: []core.Verification{
			{VerifierID: "validator-1", Signature: []byte{0x01, 0x02}, Index: 0},
		},
	}
	blk.Hash = core.BlockHash(&blk)
	cs.Chain = append(cs.Chain, blk)

	if err := core.SaveState(dir, cs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	csReload, err := core.LoadState(dir, "")
	if err != nil {
		t.Fatalf("LoadState reload: %v", err)
	}
	reloaded := csReload.Chain[len(csReload.Chain)-1]
	if len(reloaded.VerifiedSequence) != 1 {
		t.Fatalf("expected persisted verification sequence, got %d entries", len(reloaded.VerifiedSequence))
	}
	got := reloaded.VerifiedSequence[0]
	if got.VerifierID != "validator-1" || got.Index != 0 {
		t.Fatalf("unexpected verification entry: %+v", got)
	}
	if !bytes.Equal(got.Signature, blk.VerifiedSequence[0].Signature) {
		t.Fatalf("signature mismatch after reload")
	}
}
