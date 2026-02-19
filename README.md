# CoCaX-Core ⬡

A Layer-1 style blockchain node, HTTP gateway, and browser wallet built in Go.

| | |
|---|---|
| **Coin** | CoCaX (CoX) |
| **Address prefix** | `CoX` |
| **Total supply** | 33,000,000 CoX |
| **Base block reward** | 3.3 CoX |
| **Halving interval** | every 1,000,000 blocks |
| **Fixed tx fee** | 0.01 CoX |
| **Target block time** | 30 seconds |
| **Founder allocation** | 3,300,000 CoX → `CoX_FOUNDER_PLACEHOLDER_UPDATE_ME` |

---

## Project Structure

```
.
├── main.go          – CLI entrypoint + startup logic
├── models.go        – Block, Transaction, Account, ChainState types + constants
├── crypto.go        – ECDSA P-256 key ops, address derivation, signing/verification
├── storage.go       – JSON persistence (blocks.json / state.json) + genesis creation
├── api.go           – HTTP API (CORS, /balance, /tx/submit, /blocks, /mine) + MineBlock
├── network.go       – Basic P2P TCP listener + peer handshake
├── main_test.go     – Core unit tests (halving, address derivation, sign/verify, etc.)
├── api_test.go      – API tests (CORS preflight, balance, tx submit)
├── wallet/
│   ├── index.html   – CoXaNa browser wallet UI
│   ├── style.css    – Dark mode professional stylesheet
│   └── app.js       – Wallet logic (mnemonic, key derivation, signing, AES-GCM)
├── go.mod
├── LICENSE          – MIT
└── README.md
```

---

## Chain Rules / Constants

### Genesis
- **Message**: *"For united peoples working together. To hell with geography and borders, let us unite!"*
- **Founder allocation**: 3,300,000 CoX pre-allocated to `CoX_FOUNDER_PLACEHOLDER_UPDATE_ME`
- **Genesis block** is created automatically on first run if no `data/` directory exists.

### Address Derivation
```
address = "CoX" + hex(SHA-256(X_padded_32 || Y_padded_32)[:20])
```
Where X and Y are the ECDSA P-256 public key coordinates, each left-padded to 32 bytes.

### Transaction Validation
1. Signature (`sig_r`, `sig_s`) and public key (`pub_key_x`, `pub_key_y`) must be present.
2. Address derived from public key must match `from`.
3. ECDSA P-256 signature must verify against the canonical payload:
   ```
   from=<addr>&to=<addr>&amount=<%.8f>&fee=<%.8f>&nonce=<uint>&timestamp=<int>
   ```
4. `nonce` must equal `sender.nonce + 1` (replay protection).
5. `fee` must be exactly **0.01 CoX**.
6. Sender balance must be ≥ `amount + fee`.
7. Coinbase transactions cannot be submitted via the API.

### Block Reward / Halving
```
reward(blockIndex) = 3.3 / 2^(blockIndex / 1_000_000)
```
Total supply is hard-capped at 33,000,000 CoX.

### PoTC (Lightweight Proof-of-Timed-Commitment)
Each block includes a `TimedCommitment` with `validator`, `commit_time`, `reveal_deadline`, `window`, and `nonce`. Block creation is rejected if the reveal deadline has already passed.

---

## HTTP API

Base URL: `http://localhost:8080` (default)

CORS is enabled for all origins (`*`). OPTIONS pre-flight returns `204 No Content`.

### GET `/balance/{address}`
Returns address balance and nonce.

**Example:**
```bash
curl http://localhost:8080/balance/CoX_FOUNDER_PLACEHOLDER_UPDATE_ME
```
```json
{"address":"CoX_FOUNDER_PLACEHOLDER_UPDATE_ME","balance":3300000,"nonce":0}
```

### GET `/blocks`
Returns the full blockchain as a JSON array.

```bash
curl http://localhost:8080/blocks
```

### POST `/tx/submit`
Submits a signed transaction to the mempool.

**Request body:**
```json
{
  "from":      "CoX<hex>",
  "to":        "CoX<hex>",
  "amount":    1.5,
  "fee":       0.01,
  "nonce":     1,
  "timestamp": 1700000000,
  "pub_key_x": "<64-char hex>",
  "pub_key_y": "<64-char hex>",
  "sig_r":     "<hex>",
  "sig_s":     "<hex>"
}
```

**Response (200 OK):**
```json
{"mempool_size":1,"status":"accepted","tx_id":"<hex>"}
```

**Response (400 Bad Request):**
```json
{"error":"signature validation failed: missing signature"}
```

### POST `/mine`
Mines a new block from the current mempool (for development/testing).

```bash
curl -X POST http://localhost:8080/mine
```

---

## Running the Node

### Prerequisites
- Go 1.21 or later (`go version`)

### Run (all-in-one)
```bash
go run . -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data
```

### CLI Flags
| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `0.0.0.0:9000` | P2P listen address |
| `-api` | `0.0.0.0:8080` | HTTP API listen address |
| `-peers` | *(empty)* | Comma-separated peer addresses |
| `-miner` | `CoX_FOUNDER_PLACEHOLDER_UPDATE_ME` | Address to receive block rewards |
| `-mine` | `false` | Mine one block immediately on startup |
| `-data` | `./data` | Directory for `blocks.json` / `state.json` |

### Example: Two-Node Setup
```bash
# Node 1
go run . -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -data ./data1

# Node 2 (connects to node 1)
go run . -addr 0.0.0.0:9001 -api 0.0.0.0:8081 -peers localhost:9000 -data ./data2
```

---

## Wallet (CoXaNa)

The browser wallet is served from the same HTTP server:

| Scenario | URL |
|----------|-----|
| Local | `http://localhost:8080/` |
| Remote / port-forwarded | `http://<host>:8080/` |

### Features
- **Create wallet** – generates a 12-word mnemonic phrase
- **Import wallet** – enter an existing 12-word mnemonic
- **Key derivation** – mnemonic → PBKDF2 (100k iterations, SHA-256) → ECDSA P-256 private key
- **Address derivation** – consistent with the Go backend
- **Encrypted storage** – mnemonic encrypted with AES-GCM + PBKDF2 in `localStorage`
- **Dashboard** – shows address, balance, nonce (auto-refreshed from API)
- **Send** – enter recipient + amount, auto-signs and submits to `/tx/submit`
- **Receive** – displays your address with a copy button

> **Security note:** The wallet uses the Web Crypto API exclusively (no external JS dependencies). Private keys never leave the browser.

---

## Persistence

On every block commit the node writes two files to `--data` directory:

| File | Contents |
|------|----------|
| `blocks.json` | Full ordered list of all blocks |
| `state.json` | Account balances, nonces, and minted supply total |

On restart, if both files exist they are loaded directly; otherwise a fresh genesis block and founder allocation are created.

---

## Format / Test / Run

```bash
# Format
gofmt -w .

# Test
go test ./...

# Run
go run . -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data
```

---

## Known Limitations

1. **P2P sync** – Peers exchange a handshake but full block synchronisation (chain download/merge) is not implemented. The P2P layer is functional for future extension.
2. **Mnemonic compatibility** – The 256-word list is a simplified subset; it is *not* BIP-39 compatible (different word list and no checksum). Keys derived from the same mnemonic phrase will always be consistent within CoXaNa.
3. **Single-node mempool** – The mempool is in-memory only. Submitted transactions are not broadcast to peers.
4. **No PoW / PoS** – Block creation is permissioned via the `-miner` flag (intended as a lightweight PoTC demo).

---

## License

MIT – see [LICENSE](LICENSE).
