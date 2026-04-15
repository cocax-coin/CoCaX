package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
)

// DeriveAddress derives a CoCaX address (CoX<hex>) from an ECDSA P-256 public key.
// Algorithm: SHA256(X_padded_32 || Y_padded_32) -> first 20 bytes -> hex -> "CoX" prefix.
func DeriveAddress(pubKey *ecdsa.PublicKey) string {
	xHex := hex.EncodeToString(padTo32(pubKey.X.Bytes()))
	yHex := hex.EncodeToString(padTo32(pubKey.Y.Bytes()))
	addr, _ := DeriveAddressFromHex(xHex, yHex)
	return addr
}

// DeriveAddressFromHex derives a CoCaX address from hex-encoded X, Y coordinates.
func DeriveAddressFromHex(xHex, yHex string) (string, error) {
	xBytes, err := hex.DecodeString(xHex)
	if err != nil {
		return "", fmt.Errorf("invalid pub_key_x: %w", err)
	}
	yBytes, err := hex.DecodeString(yHex)
	if err != nil {
		return "", fmt.Errorf("invalid pub_key_y: %w", err)
	}
	pubBytes := append(padTo32(xBytes), padTo32(yBytes)...)
	hash := sha256.Sum256(pubBytes)
	return AddressPrefix + hex.EncodeToString(hash[:20]), nil
}

// padTo32 left-pads a byte slice to exactly 32 bytes.
func padTo32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)
	return padded
}

// GenerateKeyPair generates a new ECDSA P-256 key pair.
func GenerateKeyPair() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// TxSigningPayload returns the canonical, deterministic signing payload for a transaction.
// Uses a URL-encoded query-string format to avoid JSON floating-point representation issues.
func TxSigningPayload(tx *Transaction) []byte {
	return []byte(fmt.Sprintf(
		"from=%s&to=%s&amount=%.8f&fee=%.8f&nonce=%d&timestamp=%d",
		tx.From, tx.To, tx.Amount, tx.Fee, tx.Nonce, tx.Timestamp,
	))
}

// SignTransaction signs tx and sets PubKeyX, PubKeyY, SigR, SigS fields.
func SignTransaction(tx *Transaction, privKey *ecdsa.PrivateKey) error {
	payload := TxSigningPayload(tx)
	hash := sha256.Sum256(payload)
	r, s, err := ecdsa.Sign(rand.Reader, privKey, hash[:])
	if err != nil {
		return err
	}
	tx.SigR = hex.EncodeToString(r.Bytes())
	tx.SigS = hex.EncodeToString(s.Bytes())
	tx.PubKeyX = hex.EncodeToString(privKey.PublicKey.X.Bytes())
	tx.PubKeyY = hex.EncodeToString(privKey.PublicKey.Y.Bytes())
	return nil
}

// VerifyTransaction validates the signature, address derivation and public key of a transaction.
func VerifyTransaction(tx *Transaction) error {
	if tx.SigR == "" || tx.SigS == "" {
		return fmt.Errorf("missing signature")
	}
	if tx.PubKeyX == "" || tx.PubKeyY == "" {
		return fmt.Errorf("missing public key")
	}

	// Verify address matches the derived address.
	derivedAddr, err := DeriveAddressFromHex(tx.PubKeyX, tx.PubKeyY)
	if err != nil {
		return fmt.Errorf("address derivation failed: %w", err)
	}
	if derivedAddr != tx.From {
		return fmt.Errorf("address mismatch: derived %s, tx.from %s", derivedAddr, tx.From)
	}

	// Decode signature.
	rBytes, err := hex.DecodeString(tx.SigR)
	if err != nil {
		return fmt.Errorf("invalid sig_r: %w", err)
	}
	sBytes, err := hex.DecodeString(tx.SigS)
	if err != nil {
		return fmt.Errorf("invalid sig_s: %w", err)
	}

	// Decode public key.
	xBytes, err := hex.DecodeString(tx.PubKeyX)
	if err != nil {
		return fmt.Errorf("invalid pub_key_x: %w", err)
	}
	yBytes, err := hex.DecodeString(tx.PubKeyY)
	if err != nil {
		return fmt.Errorf("invalid pub_key_y: %w", err)
	}
	pubKey := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}

	// Verify signature.
	payload := TxSigningPayload(tx)
	hash := sha256.Sum256(payload)
	r := new(big.Int).SetBytes(rBytes)
	s := new(big.Int).SetBytes(sBytes)
	if !ecdsa.Verify(pubKey, hash[:], r, s) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// BlockHash computes a SHA-256 based hash string for the given block (excluding the hash field).
func BlockHash(b *Block) string {
	data := fmt.Sprintf(
		"%d|%s|%d|%d|%d|%s|%.8f|%s",
		b.Index,
		b.PrevHash,
		b.Timestamp,
		b.Difficulty,
		b.Nonce,
		b.Miner,
		b.Reward,
		b.Memo,
	)
	for _, tx := range b.Transactions {
		data += fmt.Sprintf("|%d:%s", tx.SequenceNumber, tx.ID)
	}
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// TxID computes a deterministic transaction ID.
func TxID(tx *Transaction) string {
	data := fmt.Sprintf("%s|%s|%.8f|%.8f|%d|%d", tx.From, tx.To, tx.Amount, tx.Fee, tx.Nonce, tx.Timestamp)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// BlockReward returns the block reward at a given block height, respecting the halving schedule.
func BlockReward(blockIndex uint64) float64 {
	halvings := blockIndex / HalvingInterval
	reward := BaseBlockReward
	for i := uint64(0); i < halvings; i++ {
		reward /= 2.0
	}
	return reward
}

// floatEqual compares two float64 values within a small epsilon.
func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// FloatEqual is an exported wrapper around floatEqual for other packages.
func FloatEqual(a, b float64) bool {
	return floatEqual(a, b)
}

// PadTo32 is an exported helper that left-pads a byte slice to 32 bytes.
func PadTo32(b []byte) []byte {
	return padTo32(b)
}
