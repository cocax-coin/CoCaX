# CoCaX-Core ⬡

A Layer-1 style blockchain core with an HTTP/RPC gateway. The wallet is a separate client that talks to the RPC server.

| | |
|---|---|
| **Coin** | CoCaX (CoX) |
| **Currency symbol** | `CoX` |
| **Chain ID** | `11121633` (`0xa9b3e1`) |
| **RPC URL** | `http://localhost:8080/rpc` |
| **Address prefix** | `CoX` |
| **Total supply** | 33,000,000 CoX |
| **Base block reward** | 3.3 CoX |
| **Halving interval** | every 1,000,000 blocks |
| **Fixed tx fee** | 0.01 CoX |
| **Target block time** | 30 seconds |
| **Founder allocation** | 3,300,000 CoX → set with `-founder <your CoX address>` |

---

## Project Structure

```
.
├── cmd/node/main.go – CLI entrypoint that wires core + RPC server
├── core/            – Blockchain core (models, crypto, storage, consensus, p2p)
├── rpc/             – HTTP RPC server (CORS, /balance, /tx/submit, /blocks, /mine)
├── wallet/          – Standalone browser wallet client (talks to RPC)
├── go.mod
└── README.md
```

---

## Chain Rules / Constants

### Genesis
- **Message**: *"For united peoples working together. To hell with geography and borders, let us unite!"*
- **Founder allocation**: 3,300,000 CoX pre-allocated to the address specified via `-founder` (see [Founder Setup](#founder-setup) below).
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

### PoVS (Parallel Validation + Slashing)
- Blocks are verified in parallel by the local node and optional peers (majority vote required).
- Every mined block records its `verifications` array (peer name, accepted flag, reason).
- The miner is **slashed** (their reward is burned) if the block is rejected by the majority.
- Coinbase must appear at `sequence = 0`; other transactions are auto-assigned contiguous `sequence` values when the block template is built (hashing includes `sequence` to lock ordering).

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
Returns the full blockchain as a JSON array. Blocks now include:
- `commitment` (PoTC), `verifications` (PoVS votes), and optional `memo`
- `transactions` entries with `is_coinbase` and `sequence` fields (coinbase is always sequence `0`)

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

### JSON-RPC `/rpc` (MetaMask-friendly)

- **RPC URL**: `http://localhost:8080/rpc`
- **Chain ID**: `11121633` (`0xa9b3e1`)
- **Currency symbol**: `CoX`
- **Supported methods**:
  - `eth_chainId` → returns `"0xa9b3e1"`
  - `eth_blockNumber` → latest block index as hex
  - `eth_getBalance` → balance of any address as hex Wei-like units (1 CoX = 1e18)
  - `eth_sendRawTransaction` → submit a hex-encoded JSON transaction (`0x{json hex}`)

---

## Running the Node

### Prerequisites
- Go 1.21 or later (`go version`)

### Run node (RPC + P2P)
```bash
go run ./cmd/node -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data
```

### CLI Flags
| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `0.0.0.0:9000` | P2P listen address |
| `-api` | `0.0.0.0:8080` | HTTP API listen address |
| `-peers` | *(empty)* | Comma-separated peer addresses |
| `-founder` | *(empty → placeholder)* | Your CoX address for the 3.3M genesis allocation |
| `-miner` | *(same as founder)* | Address to receive block rewards |
| `-mine` | `false` | Mine one block immediately on startup |
| `-data` | `./data` | Directory for `blocks.json` / `state.json` |
| `-genaddr` | `false` | Generate a new CoX address + key pair, then exit |

### Example: Two-Node Setup
```bash
# Node 1
go run ./cmd/node -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -data ./data1

# Node 2 (connects to node 1)
go run ./cmd/node -addr 0.0.0.0:9001 -api 0.0.0.0:8081 -peers localhost:9000 -data ./data2
```

---

## Wallet (CoXaNa)

The browser wallet is a standalone static client that talks to the RPC server. Host the `wallet/` directory with any static file server and point it to your node's `-api` address.

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
go run ./cmd/node -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data
```

---

## Founder Setup / إعداد عنوان المؤسس

> **English** – How to set your personal founder address and run the wallet.
> 
> **عربي** – كيف تُعدّ عنوان المؤسس الخاص بك وتشغّل المحفظة.

The node accepts **MetaMask-style `0x` addresses** for both the founder (`-founder`) and miner (`-miner`) flags. The built-in placeholder is `0x0000000000000000000000000000000000000000`; replace it with your own address on first run.

---

### Step 1 – Generate your CoX address  /  الخطوة 1 – توليد عنوانك

**Option A – Browser wallet (recommended / المُوصى به)**

1. Start the node in a **temporary** data directory so the wallet UI is reachable (this run will use the placeholder founder; it is just to access the wallet):
   ```bash
go run ./cmd/node -api 0.0.0.0:8080 -data ./data-tmp
   ```
2. Open **`http://localhost:8080/`** in your browser.
3. Click **"✦ Create New Wallet"**.
4. Write down your **12-word mnemonic phrase** on paper and store it safely.
5. Set a strong password → click **"Confirm & Unlock Wallet"**.
6. Your **CoX address** appears on the Dashboard (e.g. `CoX8f3a…`). **Copy it.**

---

**Option B – Command line / سطر الأوامر**

```bash
go run ./cmd/node -genaddr
```

Sample output:
```
================================================================
  CoCaX Address Generator  |  CoXaNa Wallet Compatible
================================================================
  CoX Address  : CoX8f3a1b2c...
  Private Key  : 7a3f...
  ...
================================================================
```

Copy the **CoX Address** line.

---

### Step 2 – Start the node with your founder address  /  الخطوة 2 – تشغيل العقدة بعنوانك

```bash
# Replace with YOUR address from Step 1
go run ./cmd/node \
  -founder CoX8f3a1b2c... \
  -addr 0.0.0.0:9000 \
  -api  0.0.0.0:8080 \
  -mine \
  -data ./data
```

On first run the node will:
- Create `./data/blocks.json` and `./data/state.json`
- Credit your address with **3,300,000 CoX** in the genesis block
- Mine block #1 (because of `-mine`) and credit your address with the block reward

---

### Step 3 – Verify your balance  /  الخطوة 3 – التحقق من رصيدك

```bash
curl http://localhost:8080/balance/CoX8f3a1b2c...
```
```json
{ "address": "CoX8f3a1b2c...", "balance": 3300003.3, "nonce": 0 }
```

Or open the wallet UI at **`http://localhost:8080/`** and log in with your mnemonic.

---

### Step 4 – Wallet walkthrough  /  الخطوة 4 – جولة في المحفظة

| Tab | What it does |
|-----|-------------|
| **Dashboard** | Shows your CoX address, live balance, and nonce |
| **Send** | Enter recipient address + amount → wallet signs locally → submits to node |
| **Receive** | Displays your address with a one-click copy button |
| **🔒 Lock** | Clears the session key; next visit requires your password |

> **Security / أمان:** The private key is **never sent to the server**. All signing happens inside the browser using the Web Crypto API. Your mnemonic is encrypted with AES-GCM before being stored in `localStorage`.

---

### Important Notes / ملاحظات مهمة

- ⚠️ **Never share your private key or mnemonic phrase with anyone.**
- ⚠️ **لا تشارك مفتاحك الخاص أو عبارة الاسترداد مع أي شخص.**
- The `-founder` flag only takes effect on the **very first run** (genesis block creation). If `./data/` already exists, delete it and restart to apply a new founder address.
- المعلّمة `-founder` تعمل فقط عند **أول تشغيل** (إنشاء كتلة البداية). إذا كان المجلد `./data/` موجوداً بالفعل، احذفه وأعد التشغيل لتطبيق عنوان مؤسس جديد.

---

## Known Limitations

1. **P2P sync** – Peers exchange a handshake but full block synchronisation (chain download/merge) is not implemented. The P2P layer is functional for future extension.
2. **Mnemonic compatibility** – The 256-word list is a simplified subset; it is *not* BIP-39 compatible (different word list and no checksum). Keys derived from the same mnemonic phrase will always be consistent within CoXaNa.
3. **Single-node mempool** – The mempool is in-memory only. Submitted transactions are not broadcast to peers.
4. **No PoW / PoS** – Block creation is permissioned via the `-miner` flag (intended as a lightweight PoTC demo).

---

## License

MIT – see [LICENSE](LICENSE).
