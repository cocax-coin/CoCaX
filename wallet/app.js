/**
 * CoXaNa – CoCaX Browser Wallet
 * Uses only the Web Crypto API (no external dependencies).
 *
 * Key design:
 *  - 12-word mnemonic from a 256-word list (96 bits of entropy)
 *  - Deterministic ECDSA P-256 key via PBKDF2 → raw scalar → PKCS8 import
 *  - Address = "CoX" + hex(SHA-256(X_32 || Y_32)[:20])  (matches backend)
 *  - Signing payload = "from=…&to=…&amount=%.8f&fee=%.8f&nonce=%d&timestamp=%d"
 *  - Encrypted mnemonic stored in localStorage (AES-GCM + PBKDF2, 100k iters)
 */

'use strict';

/* ============================================================
   Configuration
   ============================================================ */
const API_BASE = 'https://coxanas.com/api';

const FIXED_FEE   = 0.01;
const STORAGE_KEY = 'cocax_wallet_v1';
const SESSION_KEY = 'cocax_session_v1';
const PBKDF2_ITER = 100000;
const PBKDF2_HASH = 'SHA-256';

/* ============================================================
   256-Word Mnemonic List
   ============================================================ */
const WORDS = [
  'abandon','ability','able','about','above','absent','absorb','abstract',
  'absurd','abuse','access','accident','account','accuse','achieve','acid',
  'acoustic','acquire','action','actor','actress','actual','adapt','add',
  'addict','address','adjust','admit','adult','advance','advice','aerobic',
  'afford','afraid','again','agent','agree','ahead','aim','air',
  'airport','aisle','alarm','album','alert','alien','align','alley',
  'allow','almost','alone','alpha','already','always','amateur','amazing',
  'among','amount','amused','analyst','anchor','ancient','anger','angle',
  'angry','animal','ankle','announce','annual','answer','antenna','antique',
  'anxiety','apart','approve','april','arch','area','arena','argue',
  'arm','armor','army','around','arrange','arrest','arrive','arrow',
  'art','artist','artwork','ask','aspect','asset','assist','assume',
  'asthma','athlete','atom','attack','attend','attitude','attract','auction',
  'audit','august','aunt','author','auto','autumn','average','avoid',
  'awake','award','aware','away','awesome','awful','awkward','axis',
  'baby','balance','bamboo','banana','banner','barely','bargain','barrel',
  'base','basic','basket','battle','beach','beauty','become','bedroom',
  'behave','believe','below','bench','benefit','best','between','beyond',
  'bicycle','bid','bike','bind','biology','bird','birth','bitter',
  'black','blade','blame','blanket','blast','blend','bless','blind',
  'blood','blow','blue','blur','blush','board','boat','body',
  'boil','bomb','bone','bonus','book','boost','border','bounce',
  'brain','brand','brave','bread','breeze','brick','bridge','brief',
  'bright','bring','brisk','broken','bronze','brown','brush','budget',
  'build','bulb','bulk','bullet','bundle','burden','burger','burst',
  'bus','buyer','buzz','cabbage','cabin','cable','camel','camera',
  'camp','canal','cancel','candy','cannon','canvas','canyon','capable',
  'capital','captain','carbon','carpet','carry','case','castle','casual',
  'catalog','catch','category','cause','cave','ceiling','celery','cement',
  'census','century','ceramic','certain','chair','chaos','chapter','charge',
  'chase','chat','cheap','check','cheese','cherry','chest','chief',
  'child','chimney','choice','chrome','chunk','circle','citizen','city',
  'civil','claim','clap','clarify','claw','clay','clean','clerk',
  'clever','cliff','climb','clinic','clip','clock','clog','close',
  'cloth','cloud','clump','cluster','clutch','coach','coast','coconut',
  'code','coffee','coil','coin','collect','color','column','combine',
  'comfort','common','company','concert','conduct','confirm','congress','connect',
];
const WORD_SET = new Set(WORDS);

/* ============================================================
   Utility helpers
   ============================================================ */
function toHex(bytes) {
  console.log('toHex start');
  return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
}

function fromHex(hex) {
  console.log('fromHex start');
  const bytes = new Uint8Array(Math.ceil(hex.length / 2));
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return bytes;
}

function b64urlToBytes(b64url) {
  console.log('b64urlToBytes start');
  const b64 = b64url.replace(/-/g, '+').replace(/_/g, '/');
  const padded = b64.padEnd(b64.length + (4 - b64.length % 4) % 4, '=');
  const binary = atob(padded);
  return Uint8Array.from(binary, c => c.charCodeAt(0));
}

function padTo32(bytes) {
  console.log('padTo32 start');
  if (bytes.length >= 32) return bytes.slice(bytes.length - 32);
  const out = new Uint8Array(32);
  out.set(bytes, 32 - bytes.length);
  return out;
}

function showMsg(id, msg, type = 'info') {
  console.log('showMsg start');
  const el = document.getElementById(id);
  if (!el) return;
  el.innerHTML = msg
    ? `<div class="alert alert-${type}">${msg}</div>`
    : '';
}

async function copyText(text) {
  console.log('copyText start');
  try { await navigator.clipboard.writeText(text); return true; }
  catch (_) { return false; }
}

/* ============================================================
   Crypto helpers
   ============================================================ */

/**
 * Build the PKCS#8 DER encoding for a raw 32-byte P-256 private key scalar.
 * Structure (RFC 5958 / RFC 5915):
 *   SEQUENCE { INTEGER 0  AlgorithmIdentifier  OCTET STRING { ECPrivateKey } }
 */
function buildPKCS8(privBytes) {
  console.log('buildPKCS8 start');
  const hdr = new Uint8Array([
    0x30, 0x41,                                     // SEQUENCE, length 65
    0x02, 0x01, 0x00,                               // INTEGER 0 (version)
    0x30, 0x13,                                     // AlgorithmIdentifier, length 19
      0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01, // OID ecPublicKey
      0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, // OID P-256
    0x04, 0x27,                                     // OCTET STRING, length 39
      0x30, 0x25,                                   // ECPrivateKey SEQUENCE, length 37
        0x02, 0x01, 0x01,                           // INTEGER 1
        0x04, 0x20,                                 // OCTET STRING, length 32
  ]);
  const der = new Uint8Array(hdr.length + 32);
  der.set(hdr);
  der.set(privBytes.slice(0, 32), hdr.length);
  return der.buffer;
}

/**
 * Derive a deterministic 32-byte key from a mnemonic string using PBKDF2.
 * Salt is the UTF-8 encoding of "CoCaX-v1-key".
 */
async function mnemonicToPrivBytes(mnemonic) {
  console.log('mnemonicToPrivBytes start');
  const enc = new TextEncoder();
  const keyMaterial = await crypto.subtle.importKey(
    'raw', enc.encode(mnemonic.trim().toLowerCase()),
    { name: 'PBKDF2' }, false, ['deriveBits']
  );
  const bits = await crypto.subtle.deriveBits(
    { name: 'PBKDF2', salt: enc.encode('CoCaX-v1-key'), iterations: PBKDF2_ITER, hash: PBKDF2_HASH },
    keyMaterial, 256
  );
  return new Uint8Array(bits);
}

/**
 * Import a 32-byte private scalar as an ECDSA P-256 CryptoKey (signing key).
 */
async function importPrivKey(privBytes) {
  console.log('importPrivKey start');
  return crypto.subtle.importKey(
    'pkcs8', buildPKCS8(privBytes),
    { name: 'ECDSA', namedCurve: 'P-256' },
    true, ['sign']
  );
}

/**
 * Export the public key coordinates (x, y) from a private CryptoKey as hex strings.
 */
async function exportPubKeyHex(privateKey) {
  console.log('exportPubKeyHex start');
  const jwk = await crypto.subtle.exportKey('jwk', privateKey);
  const xBytes = padTo32(b64urlToBytes(jwk.x));
  const yBytes = padTo32(b64urlToBytes(jwk.y));
  return { xHex: toHex(xBytes), yHex: toHex(yBytes) };
}

/**
 * Derive the CoX address from hex-encoded public key coordinates.
 * Must match the Go backend: SHA-256(X_32 || Y_32) → first 20 bytes → hex → "CoX" prefix.
 */
async function deriveAddress(xHex, yHex) {
  console.log('deriveAddress start');
  const xBytes = padTo32(fromHex(xHex));
  const yBytes = padTo32(fromHex(yHex));
  const pubBytes = new Uint8Array(64);
  pubBytes.set(xBytes, 0);
  pubBytes.set(yBytes, 32);
  const hash = await crypto.subtle.digest('SHA-256', pubBytes);
  return 'CoX' + toHex(new Uint8Array(hash).slice(0, 20));
}

/**
 * Build the canonical transaction signing payload (must match Go TxSigningPayload).
 * Format: "from=…&to=…&amount=<8dp>&fee=<8dp>&nonce=<uint>&timestamp=<int>"
 */
function txSigningPayload(tx) {
  console.log('txSigningPayload start');
  return `from=${tx.from}&to=${tx.to}&amount=${Number(tx.amount).toFixed(8)}&fee=${Number(tx.fee).toFixed(8)}&nonce=${tx.nonce}&timestamp=${tx.timestamp}`;
}

/**
 * Sign a transaction object, returning it with sig_r, sig_s, pub_key_x, pub_key_y set.
 */
async function signTransaction(tx, privateKey, xHex, yHex) {
  console.log('signTransaction start');
  const payload = txSigningPayload(tx);
  const data = new TextEncoder().encode(payload);
  const sigBuf = await crypto.subtle.sign(
    { name: 'ECDSA', hash: { name: 'SHA-256' } },
    privateKey, data
  );
  const sig = new Uint8Array(sigBuf);
  return {
    ...tx,
    pub_key_x: xHex,
    pub_key_y: yHex,
    sig_r: toHex(sig.slice(0, 32)),
    sig_s: toHex(sig.slice(32, 64)),
  };
}

/* ============================================================
   Wallet encryption / decryption (AES-GCM + PBKDF2)
   ============================================================ */

async function encryptMnemonic(mnemonic, password) {
  console.log('encryptMnemonic start');
  const enc = new TextEncoder();
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iv   = crypto.getRandomValues(new Uint8Array(12));

  const keyMaterial = await crypto.subtle.importKey(
    'raw', enc.encode(password), { name: 'PBKDF2' }, false, ['deriveKey']
  );
  const aesKey = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt, iterations: PBKDF2_ITER, hash: PBKDF2_HASH },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    false, ['encrypt']
  );
  const ciphertext = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv },
    aesKey,
    enc.encode(mnemonic)
  );
  return {
    ciphertext: toHex(new Uint8Array(ciphertext)),
    salt: toHex(salt),
    iv: toHex(iv),
  };
}

async function decryptMnemonic(encrypted, password) {
  console.log('decryptMnemonic start');
  const enc = new TextEncoder();
  const salt       = fromHex(encrypted.salt);
  const iv         = fromHex(encrypted.iv);
  const ciphertext = fromHex(encrypted.ciphertext);

  const keyMaterial = await crypto.subtle.importKey(
    'raw', enc.encode(password), { name: 'PBKDF2' }, false, ['deriveKey']
  );
  const aesKey = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt, iterations: PBKDF2_ITER, hash: PBKDF2_HASH },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    false, ['decrypt']
  );
  const plaintext = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv },
    aesKey,
    ciphertext
  );
  return new TextDecoder().decode(plaintext);
}

/* ============================================================
   Mnemonic generation
   ============================================================ */
function generateMnemonic() {
  console.log('generateMnemonic start');
  const words = [];
  const arr = new Uint8Array(12);
  crypto.getRandomValues(arr);
  for (let i = 0; i < 12; i++) {
    words.push(WORDS[arr[i] % WORDS.length]);
  }
  return words.join(' ');
}

function validateSeedWords(words) {
  console.log('validateSeedWords start');
  if (!(words.length === 12 || words.length === 24)) return false;
  return words.every(w => WORD_SET.has(w));
}

/* ============================================================
   Wallet state (in-memory session only)
   ============================================================ */
const wallet = {
  privateKey: null,  // CryptoKey
  address:    '',
  pubKeyX:    '',
  pubKeyY:    '',
  balance:    0,
  nonce:      0,
};

/* ============================================================
   localStorage helpers
   ============================================================ */
function loadStorage() {
  console.log('loadStorage start');
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY)); }
  catch (_) { return null; }
}

function saveStorage(data) {
  console.log('saveStorage start');
  localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
}

function clearStorage() {
  console.log('clearStorage start');
  localStorage.removeItem(STORAGE_KEY);
}

function setSession(data) {
  console.log('setSession start');
  sessionStorage.setItem(SESSION_KEY, JSON.stringify(data));
}

function getSession() {
  console.log('getSession start');
  try { return JSON.parse(sessionStorage.getItem(SESSION_KEY)); }
  catch (_) { return null; }
}

function clearSession() {
  console.log('clearSession start');
  sessionStorage.removeItem(SESSION_KEY);
}

function credentialError(msg) {
  console.log('credentialError start');
  const err = new Error('Invalid Credentials');
  err.userMessage = 'Invalid Credentials';
  if (msg) err.userMessage = msg;
  return err;
}

/* ============================================================
   API helpers
   ============================================================ */
async function fetchBalance(address) {
  console.log('fetchBalance start');
  const res = await fetch(`${API_BASE}/balance/${address}`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

async function submitTx(tx) {
  console.log('submitTx start');
  const res = await fetch(`${API_BASE}/tx/submit`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(tx),
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

/* ============================================================
   UI helpers
   ============================================================ */
function showScreen(id) {
  console.log('showScreen start');
  for (const s of ['setup-screen', 'lock-screen', 'wallet-screen']) {
    document.getElementById(s).style.display = (s === id) ? '' : 'none';
  }
}

function showTab(name) {
  console.log('showTab start');
  document.querySelectorAll('.tab-btn').forEach(b => {
    if (b.dataset.tab) b.classList.toggle('active', b.dataset.tab === name);
  });
  document.querySelectorAll('.tab-pane').forEach(p => {
    p.classList.toggle('active', p.id === `tab-${name}`);
  });
}

function updateDashboard() {
  console.log('updateDashboard start');
  document.getElementById('dash-address').textContent = wallet.address;
  document.getElementById('dash-balance').textContent = `${wallet.balance.toFixed(4)} CoX`;
  document.getElementById('dash-nonce').textContent   = wallet.nonce;
  document.getElementById('recv-address').textContent = wallet.address;
}

async function refreshBalance() {
  console.log('refreshBalance start');
  try {
    const data = await fetchBalance(wallet.address);
    wallet.balance = data.balance || 0;
    wallet.nonce   = data.nonce   || 0;
    updateDashboard();
    showMsg('dash-msg', '✓ Balance refreshed', 'success');
    setTimeout(() => showMsg('dash-msg', ''), 2000);

    // Update network badge
    const badge = document.getElementById('network-badge');
    badge.textContent = 'online';
    badge.classList.add('online');
  } catch (e) {
    showMsg('dash-msg', `Failed to fetch balance: ${e.message}`, 'error');

    const badge = document.getElementById('network-badge');
    badge.textContent = 'offline';
    badge.classList.remove('online');
  }
}

/* ============================================================
   Wallet builders
   ============================================================ */
async function buildWalletFromPrivBytes(privBytes) {
  console.log('buildWalletFromPrivBytes start');
  if (!(privBytes instanceof Uint8Array)) throw credentialError('Private key must be bytes.');
  if (privBytes.length < 32) throw credentialError('Invalid Credentials');
  const privateKey = await importPrivKey(privBytes);
  const { xHex, yHex } = await exportPubKeyHex(privateKey);
  const address = await deriveAddress(xHex, yHex);
  return {
    privateKey,
    address,
    pubKeyX: xHex,
    pubKeyY: yHex,
    privHex: toHex(privBytes.slice(privBytes.length - 32)),
    balance: 0,
    nonce: 0,
  };
}

async function deriveWalletFromMnemonic(mnemonicInput) {
  console.log('deriveWalletFromMnemonic start');
  const normalized = mnemonicInput.trim().toLowerCase().split(/\s+/);
  if (!validateSeedWords(normalized)) throw credentialError();
  const mnemonic = normalized.join(' ');
  const privBytes = await mnemonicToPrivBytes(mnemonic);
  const wallet = await buildWalletFromPrivBytes(privBytes);
  wallet.mnemonic = mnemonic;
  return wallet;
}

async function deriveWalletFromPrivHex(privHex) {
  console.log('deriveWalletFromPrivHex start');
  if (!/^[0-9a-fA-F]{64}$/.test(privHex.trim())) {
    throw credentialError();
  }
  const privBytes = fromHex(privHex.trim());
  if (privBytes.length !== 32) throw credentialError();
  return buildWalletFromPrivBytes(privBytes);
}

/* ============================================================
   Session-aware UI flows
   ============================================================ */
function setupEntryPage() {
  console.log('setupEntryPage start');
  const root = document.getElementById('entry-root');
  if (!root) return;

  const genBtn = document.getElementById('btn-generate');
  const genOut = document.getElementById('generate-output');
  const genMnemonic = document.getElementById('generated-mnemonic');
  const genAddr = document.getElementById('generated-address');
  const genPriv = document.getElementById('generated-privkey');
  const genUse = document.getElementById('btn-use-generated');

  let pendingWallet = null;

  genBtn?.addEventListener('click', async () => {
    pendingWallet = await deriveWalletFromMnemonic(generateMnemonic());
    const words = pendingWallet.mnemonic.split(' ');
    genMnemonic.innerHTML = words.map((w, i) =>
      `<div class="mnemonic-word"><span class="idx">${i + 1}.</span>${w}</div>`
    ).join('');
    genAddr.textContent = pendingWallet.address;
    genPriv.textContent = pendingWallet.privHex;
    genOut.style.display = '';
    showMsg('generate-msg', 'Wallet generated. Save the seed phrase securely.', 'info');
  });

  genUse?.addEventListener('click', () => {
    if (!pendingWallet) {
      showMsg('generate-msg', 'Generate a wallet first.', 'error');
      return;
    }
    setSession({
      privHex: pendingWallet.privHex,
      address: pendingWallet.address,
      pubKeyX: pendingWallet.pubKeyX,
      pubKeyY: pendingWallet.pubKeyY,
    });
    window.location.href = 'dashboard.html';
  });

  document.getElementById('btn-import-seed')?.addEventListener('click', async () => {
    const seed = document.getElementById('import-seed').value;
    try {
      const w = await deriveWalletFromMnemonic(seed);
      setSession({
        privHex: w.privHex,
        address: w.address,
        pubKeyX: w.pubKeyX,
        pubKeyY: w.pubKeyY,
      });
      window.location.href = 'dashboard.html';
    } catch (e) {
      showMsg('import-seed-msg', e.userMessage || e.message || 'Invalid Credentials', 'error');
    }
  });

  document.getElementById('btn-login-privkey')?.addEventListener('click', async () => {
    const pk = document.getElementById('login-privkey').value;
    try {
      const w = await deriveWalletFromPrivHex(pk);
      setSession({
        privHex: w.privHex,
        address: w.address,
        pubKeyX: w.pubKeyX,
        pubKeyY: w.pubKeyY,
      });
      window.location.href = 'dashboard.html';
    } catch (e) {
      showMsg('login-privkey-msg', e.userMessage || e.message || 'Invalid Credentials', 'error');
    }
  });
}

function setupDashboardPage() {
  console.log('setupDashboardPage start');
  const root = document.getElementById('dashboard-root');
  if (!root) return;

  const session = getSession();
  if (!session || !session.privHex) {
    window.location.href = 'index.html';
    return;
  }

  const walletState = { ...wallet };

  async function hydrateFromSession() {
    const built = await deriveWalletFromPrivHex(session.privHex);
    walletState.privateKey = built.privateKey;
    walletState.address = built.address;
    walletState.pubKeyX = built.pubKeyX;
    walletState.pubKeyY = built.pubKeyY;
    walletState.balance = 0;
    walletState.nonce = 0;
    updateDashboardUI(walletState);
    await refreshBalanceUI(walletState);
  }

  document.getElementById('btn-refresh')?.addEventListener('click', () => refreshBalanceUI(walletState));
  ['btn-copy-addr', 'btn-copy-addr-top'].forEach(id => {
    const btn = document.getElementById(id);
    if (!btn) return;
    btn.addEventListener('click', async () => {
      const ok = await copyText(walletState.address);
      showMsg('recv-msg', ok ? '✓ Address copied!' : 'Copy failed', ok ? 'success' : 'error');
      if (ok) setTimeout(() => showMsg('recv-msg', ''), 2000);
    });
  });

  document.getElementById('btn-back')?.addEventListener('click', () => {
    clearSession();
    window.location.href = 'index.html';
  });

  document.getElementById('btn-send')?.addEventListener('click', async () => {
    const to     = document.getElementById('send-to').value.trim();
    const amount = parseFloat(document.getElementById('send-amount').value);

    if (!to.startsWith('CoX') || to.length < 10) {
      showMsg('send-msg', 'Invalid recipient address (must start with CoX).', 'error');
      return;
    }
    if (isNaN(amount) || amount <= 0) {
      showMsg('send-msg', 'Enter a valid amount.', 'error');
      return;
    }
    if (walletState.balance < amount + FIXED_FEE) {
      showMsg('send-msg', `Insufficient balance. Need ${(amount + FIXED_FEE).toFixed(8)} CoX.`, 'error');
      return;
    }
    if (!walletState.privateKey) {
      showMsg('send-msg', 'Wallet is locked.', 'error');
      return;
    }

    try {
      const balData = await fetchBalance(walletState.address);
      walletState.nonce = balData.nonce || 0;
      updateDashboardUI(walletState);

      const tx = {
        from:      walletState.address,
        to,
        amount:    Number(amount.toFixed(8)),
        fee:       FIXED_FEE,
        nonce:     walletState.nonce + 1,
        timestamp: Math.floor(Date.now() / 1000),
        is_coinbase: false,
      };

      const signedTx = await signTransaction(tx, walletState.privateKey, walletState.pubKeyX, walletState.pubKeyY);
      const result   = await submitTx(signedTx);

      walletState.nonce += 1;
      walletState.balance = Math.max(0, walletState.balance - amount - FIXED_FEE);
      updateDashboardUI(walletState);

      showMsg('send-msg',
        `✓ Transaction accepted! TX ID: <code style="word-break:break-all">${result.tx_id || '(pending)'}</code><br>Mempool size: ${result.mempool_size}`,
        'success'
      );
      document.getElementById('send-to').value     = '';
      document.getElementById('send-amount').value = '';
    } catch (e) {
      showMsg('send-msg', `Send failed: ${e.message}`, 'error');
    }
  });

  hydrateFromSession().catch(err => {
    console.error(err);
    clearSession();
    window.location.href = 'index.html';
  });
}

/* ============================================================
   Dashboard helpers
   ============================================================ */
function updateDashboardUI(state) {
  console.log('updateDashboardUI start');
  if (document.getElementById('dash-address')) {
    document.getElementById('dash-address').textContent = state.address;
  }
  if (document.getElementById('dash-balance')) {
    document.getElementById('dash-balance').textContent = `${state.balance.toFixed(4)} CoX`;
  }
  if (document.getElementById('dash-nonce')) {
    document.getElementById('dash-nonce').textContent = state.nonce;
  }
  if (document.getElementById('recv-address')) {
    document.getElementById('recv-address').textContent = state.address;
  }
}

async function refreshBalanceUI(state) {
  console.log('refreshBalanceUI start');
  try {
    const data = await fetchBalance(state.address);
    state.balance = data.balance || 0;
    state.nonce   = data.nonce   || 0;
    updateDashboardUI(state);
    showMsg('dash-msg', '✓ Balance refreshed', 'success');
    setTimeout(() => showMsg('dash-msg', ''), 2000);
  } catch (e) {
    showMsg('dash-msg', `Failed to fetch balance: ${e.message}`, 'error');
  }
}

/* ============================================================
   Startup
   ============================================================ */
(() => {
  setupEntryPage();
  setupDashboardPage();
})();
