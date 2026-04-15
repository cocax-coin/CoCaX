package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// P2PNode handles peer-to-peer connectivity over raw TCP.
type P2PNode struct {
	ListenAddr string
	Peers      []string
	state      *ChainState
	dataDir    string

	mu                  sync.Mutex
	seenTxIDs           map[string]time.Time
	seenBlockHashes     map[string]time.Time
	knownBlockHashes    map[string]struct{}
	finalizedBroadcasts map[string]bool
	indexedHeight       int
}

// P2PMessage is the wire format used between peers.
type P2PMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type helloPayload struct {
	ChainLength int `json:"chain_length"`
}

type getBlocksRequest struct {
	From int `json:"from"`
}

// VerifyBlockRequest is sent to peers for PoVS validation.
type VerifyBlockRequest struct {
	BlockID   string `json:"block_id"`
	BlockData Block  `json:"block_data"`
}

// VerifyBlockResponse returns a peer's validation vote.
type VerifyBlockResponse struct {
	BlockID string `json:"block_id"`
	Accept  bool   `json:"accept"`
	Reason  string `json:"reason,omitempty"`
}

const (
	seenCacheLimit = 2048
	seenCacheTTL   = 30 * time.Minute
)

var errFullSyncRequired = errors.New("full sync required")

// NewP2PNode creates a new P2PNode.
func NewP2PNode(listenAddr string, peers []string, state *ChainState, dataDir string) *P2PNode {
	node := &P2PNode{
		ListenAddr:          listenAddr,
		Peers:               peers,
		state:               state,
		dataDir:             dataDir,
		seenTxIDs:           make(map[string]time.Time),
		seenBlockHashes:     make(map[string]time.Time),
		knownBlockHashes:    make(map[string]struct{}),
		finalizedBroadcasts: make(map[string]bool),
	}
	node.indexExistingBlocks()
	return node
}

func (n *P2PNode) indexExistingBlocks() {
	if n.state == nil {
		return
	}
	n.state.RLock()
	chainCopy := make([]Block, len(n.state.Chain))
	copy(chainCopy, n.state.Chain)
	n.state.RUnlock()

	now := time.Now()
	n.mu.Lock()
	for _, blk := range chainCopy {
		if blk.Hash == "" {
			continue
		}
		n.knownBlockHashes[blk.Hash] = struct{}{}
		n.seenBlockHashes[blk.Hash] = now
		if blk.Finalized {
			n.finalizedBroadcasts[blk.Hash] = true
		}
	}
	n.indexedHeight = len(chainCopy)
	n.mu.Unlock()
}

// Start begins listening for inbound connections and dials configured peers.
func (n *P2PNode) Start() {
	ln, err := net.Listen("tcp", n.ListenAddr)
	if err != nil {
		log.Printf("[P2P] Failed to listen on %s: %v", n.ListenAddr, err)
		return
	}
	log.Printf("[P2P] Listening on %s", n.ListenAddr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				continue
			}
			go n.handleConn(conn)
		}
	}()

	for _, peer := range n.Peers {
		p := strings.TrimSpace(peer)
		if p != "" && p != n.ListenAddr {
			go n.connectPeer(p)
		}
	}
}

// handleConn handles an inbound peer connection.
func (n *P2PNode) handleConn(conn net.Conn) {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return
	}

	n.state.mu.RLock()
	chainLen := len(n.state.Chain)
	n.state.mu.RUnlock()

	hello := P2PMessage{Type: "hello"}
	helloPayload, err := json.Marshal(map[string]interface{}{
		"node":         "CoCaX-Core",
		"chain_id":     ChainID,
		"chain_length": chainLen,
	})
	if err != nil {
		log.Printf("[P2P] Failed to marshal hello payload: %v", err)
		return
	}
	hello.Payload = helloPayload

	enc := json.NewEncoder(conn)
	if err := enc.Encode(hello); err != nil {
		return
	}

	dec := json.NewDecoder(conn)
	for {
		var msg P2PMessage
		if err := dec.Decode(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "hello":
			log.Printf("[P2P] Peer connected from %s", conn.RemoteAddr())
		case "get_blocks":
			var req getBlocksRequest
			if len(msg.Payload) > 0 {
				_ = json.Unmarshal(msg.Payload, &req)
			}
			if req.From < 0 {
				req.From = 0
			}
			n.state.mu.RLock()
			if req.From > len(n.state.Chain) {
				req.From = len(n.state.Chain)
			}
			blocks := n.state.Chain[req.From:]
			n.state.mu.RUnlock()
			resp := P2PMessage{Type: "blocks"}
			data, _ := json.Marshal(blocks)
			resp.Payload = data
			if err := enc.Encode(resp); err != nil {
				log.Printf("[P2P] Failed to send blocks: %v", err)
			}
		case "verify_block":
			var req VerifyBlockRequest
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				log.Printf("[P2P] Failed to decode verify request: %v", err)
				return
			}
			err := VerifyBlock(n.state, &req.BlockData)
			if err != nil {
				log.Printf("[P2P] verify_block failed: %v", err)
			}
			resp := P2PMessage{Type: "verify_block_response"}
			body := VerifyBlockResponse{BlockID: req.BlockID, Accept: err == nil}
			if err != nil {
				body.Reason = err.Error()
			}
			respPayload, payloadErr := json.Marshal(body)
			if payloadErr != nil {
				log.Printf("[P2P] Failed to marshal verify response: %v", payloadErr)
				return
			}
			resp.Payload = respPayload
			if err := enc.Encode(resp); err != nil {
				log.Printf("[P2P] Failed to send verify response: %v", err)
			}
		case "tx":
			var tx Transaction
			if err := json.Unmarshal(msg.Payload, &tx); err != nil {
				log.Printf("[P2P] Failed to decode tx: %v", err)
				return
			}
			if tx.ID == "" {
				tx.ID = TxID(&tx)
			}
			if n.hasSeenTx(tx.ID) {
				return
			}
			if err := AddTxToMempool(n.state, &tx); err != nil {
				log.Printf("[P2P] Rejected tx %s: %v", tx.ID, err)
				return
			}
			n.BroadcastTx(&tx)
		case "block":
			var blk Block
			if err := json.Unmarshal(msg.Payload, &blk); err != nil {
				log.Printf("[P2P] Failed to decode block: %v", err)
				return
			}
			if blk.Hash == "" {
				blk.Hash = BlockHash(&blk)
			}
			if n.tryFinalizeExistingBlock(&blk) {
				n.BroadcastBlock(&blk)
				return
			}
			if n.hasSeenBlock(blk.Hash) || n.chainHasBlock(blk.Hash) {
				return
			}
			adopted, err := ResolveFork(n.state, &blk, n.dataDir)
			if err != nil {
				log.Printf("[P2P] Fork resolution failed: %v", err)
				return
			}
			if adopted {
				n.BroadcastBlock(&blk)
				return
			}
			// If the candidate was not adopted as a finalized fork, process it as a
			// standard block addition.
			if err := AddBlock(n.state, &blk, n.dataDir); err != nil {
				log.Printf("[P2P] Rejected block %s: %v", blk.Hash, err)
				return
			}
			n.BroadcastBlock(&blk)
		default:
			log.Printf("[P2P] Unknown message type: %s", msg.Type)
		}
	}
}

func (n *P2PNode) tryFinalizeExistingBlock(block *Block) bool {
	if block == nil || !block.Finalized || block.Hash == "" {
		return false
	}
	n.state.Lock()
	defer n.state.Unlock()
	updated := false
	for i := range n.state.Chain {
		if n.state.Chain[i].Hash == block.Hash {
			if !n.state.Chain[i].Finalized {
				n.state.Chain[i].Finalized = true
				updated = true
			}
			break
		}
	}
	if updated && n.dataDir != "" {
		if err := SaveState(n.dataDir, n.state); err != nil {
			log.Printf("[P2P] Failed to persist finalized block: %v", err)
		}
	}
	if updated {
		n.mu.Lock()
		if n.finalizedBroadcasts == nil {
			n.finalizedBroadcasts = make(map[string]bool)
		}
		n.finalizedBroadcasts[block.Hash] = true
		n.mu.Unlock()
	}
	return updated
}

// connectPeer dials a remote peer and performs the handshake.
func (n *P2PNode) connectPeer(addr string) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		log.Printf("[P2P] Failed to connect to peer %s: %v", addr, err)
		return
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return
	}

	log.Printf("[P2P] Connected to peer %s", addr)

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	// Read peer's hello.
	var msg P2PMessage
	if err := dec.Decode(&msg); err != nil {
		return
	}
	var peerHello helloPayload
	if msg.Type == "hello" && len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &peerHello)
	}

	// Respond with our hello.
	n.state.mu.RLock()
	chainLen := len(n.state.Chain)
	n.state.mu.RUnlock()

	reply := P2PMessage{Type: "hello"}
	replyPayload, err := json.Marshal(map[string]interface{}{
		"node":         "CoCaX-Core",
		"chain_id":     ChainID,
		"chain_length": chainLen,
	})
	if err != nil {
		log.Printf("[P2P] Failed to marshal hello payload: %v", err)
		return
	}
	reply.Payload = replyPayload
	if err := enc.Encode(reply); err != nil {
		log.Printf("[P2P] Failed to send hello to %s: %v", addr, err)
		return
	}

	fmt.Printf("[P2P] Handshake complete with %s (peer msg type: %s)\n", addr, msg.Type)

	if peerHello.ChainLength < chainLen {
		return
	}
	// When lengths are equal, request from genesis (from=0) so both sides can
	// compare full cumulative work and still converge on the heavier chain.
	syncFrom := 0
	if peerHello.ChainLength > chainLen {
		syncFrom = chainLen
	}
	if err := n.requestBlocks(dec, enc, syncFrom, addr); err != nil {
		if errors.Is(err, errFullSyncRequired) {
			if fullErr := n.fetchBlocksFresh(addr, 0); fullErr != nil {
				log.Printf("[P2P] Failed to full-sync from %s: %v", addr, fullErr)
			}
			return
		}
		log.Printf("[P2P] Failed to sync from %s: %v", addr, err)
	}
}

func (n *P2PNode) requestBlocks(dec *json.Decoder, enc *json.Encoder, from int, addr string) error {
	reqPayload, _ := json.Marshal(getBlocksRequest{From: from})
	req := P2PMessage{Type: "get_blocks", Payload: reqPayload}
	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("send get_blocks to %s: %w", addr, err)
	}

	var resp P2PMessage
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("read get_blocks response from %s: %w", addr, err)
	}
	if resp.Type != "blocks" {
		return fmt.Errorf("unexpected response type %s from %s", resp.Type, addr)
	}

	var blocks []Block
	if err := json.Unmarshal(resp.Payload, &blocks); err != nil {
		return fmt.Errorf("decode blocks from %s: %w", addr, err)
	}
	if len(blocks) == 0 {
		return nil
	}
	return n.applySyncedBlocks(blocks)
}

func (n *P2PNode) applySyncedBlocks(blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}

	n.state.RLock()
	localLen := len(n.state.Chain)
	var localTipHash string
	if localLen > 0 {
		localTipHash = n.state.Chain[localLen-1].Hash
	}
	localChain := make([]Block, localLen)
	copy(localChain, n.state.Chain)
	n.state.RUnlock()

	// If the peer sent a full chain, consider full replacement with safety
	// against finalized rollbacks.
	if blocks[0].Index == 0 {
		return n.replaceChain(blocks, localChain)
	}

	// Fast-path append when chains share the same tip.
	if int(blocks[0].Index) != localLen || blocks[0].PrevHash != localTipHash {
		// Fallback to full chain replacement attempt.
		return errFullSyncRequired
	}

	for i := range blocks {
		blk := blocks[i]
		if err := AddBlock(n.state, &blk, n.dataDir); err != nil {
			return fmt.Errorf("append block %d: %w", blk.Index, err)
		}
	}
	return nil
}

func (n *P2PNode) fetchBlocksFresh(addr string, from int) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var peerHello P2PMessage
	if err := dec.Decode(&peerHello); err != nil {
		return err
	}
	if peerHello.Type != "hello" {
		return fmt.Errorf("expected hello from %s, got %s", addr, peerHello.Type)
	}

	n.state.mu.RLock()
	chainLen := len(n.state.Chain)
	n.state.mu.RUnlock()

	replyPayload, _ := json.Marshal(map[string]interface{}{
		"node":         "CoCaX-Core",
		"chain_id":     ChainID,
		"chain_length": chainLen,
	})
	if err := enc.Encode(P2PMessage{Type: "hello", Payload: replyPayload}); err != nil {
		return err
	}
	return n.requestBlocks(dec, enc, from, addr)
}

func (n *P2PNode) replaceChain(newChain []Block, localChain []Block) error {
	lastFinalized := -1
	for i := range localChain {
		if localChain[i].Finalized {
			lastFinalized = i
		}
	}
	if lastFinalized >= 0 {
		if len(newChain) <= lastFinalized || newChain[lastFinalized].Hash != localChain[lastFinalized].Hash {
			return fmt.Errorf("peer chain diverges before finalized block %d", lastFinalized)
		}
	}

	if TotalDifficulty(newChain).Cmp(TotalDifficulty(localChain)) <= 0 {
		return nil
	}

	rebuilt, err := rebuildStateFromChain(newChain)
	if err != nil {
		return fmt.Errorf("rebuild from peer chain failed: %w", err)
	}

	n.state.Lock()
	n.state.Chain = rebuilt.Chain
	n.state.Accounts = rebuilt.Accounts
	n.state.MintedSupply = rebuilt.MintedSupply
	// preserve local mempool to avoid losing pending txs during sync
	n.state.Unlock()

	n.indexExistingBlocks()

	if n.dataDir != "" {
		n.state.RLock()
		if err := SaveState(n.dataDir, n.state); err != nil {
			log.Printf("[P2P] Failed to persist synced chain: %v", err)
		}
		n.state.RUnlock()
	}
	return nil
}

// BroadcastTx propagates a validated transaction to all configured peers while
// suppressing rebroadcast loops via an in-memory ID cache.
func (n *P2PNode) BroadcastTx(tx *Transaction) {
	if tx == nil {
		return
	}
	if tx.ID == "" {
		tx.ID = TxID(tx)
	}
	if n.hasSeenTx(tx.ID) {
		return
	}
	n.markTxSeen(tx.ID)
	n.broadcastMessage("tx", tx)
}

// BroadcastBlock propagates a validated block to peers, avoiding redundant
// rebroadcasts via an in-memory hash cache.
func (n *P2PNode) BroadcastBlock(block *Block) {
	if block == nil {
		return
	}
	if block.Hash == "" {
		block.Hash = BlockHash(block)
	}
	n.mu.Lock()
	alreadyFinalized := n.finalizedBroadcasts[block.Hash]
	if block.Finalized && !alreadyFinalized {
		n.finalizedBroadcasts[block.Hash] = true
	}
	forceBroadcast := block.Finalized && !alreadyFinalized
	n.mu.Unlock()

	if !forceBroadcast && n.hasSeenBlock(block.Hash) {
		return
	}
	n.recordBlockHash(block.Hash)
	n.broadcastMessage("block", block)
}

func (n *P2PNode) broadcastMessage(msgType string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[P2P] Failed to marshal %s payload: %v", msgType, err)
		return
	}
	msg := P2PMessage{Type: msgType, Payload: data}
	for _, peer := range n.Peers {
		p := strings.TrimSpace(peer)
		if p == "" || p == n.ListenAddr {
			continue
		}
		go n.sendMessage(p, msg)
	}
}

func (n *P2PNode) sendMessage(addr string, msg P2PMessage) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		log.Printf("[P2P] Failed to connect to %s: %v", addr, err)
		return
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return
	}

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var hello P2PMessage
	if err := dec.Decode(&hello); err != nil {
		log.Printf("[P2P] Failed to read hello from %s: %v", addr, err)
		return
	}

	if err := enc.Encode(msg); err != nil {
		log.Printf("[P2P] Failed to send %s to %s: %v", msg.Type, addr, err)
	}
}

func (n *P2PNode) hasSeenTx(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hasSeenLocked(n.seenTxIDs, id)
}

func (n *P2PNode) hasSeenBlock(hash string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hasSeenLocked(n.seenBlockHashes, hash)
}

func (n *P2PNode) hasSeenLocked(store map[string]time.Time, key string) bool {
	if store == nil {
		return false
	}
	if ts, ok := store[key]; ok {
		if time.Since(ts) < seenCacheTTL {
			return true
		}
		delete(store, key)
	}
	return false
}

func (n *P2PNode) markTxSeen(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seenTxIDs == nil {
		n.seenTxIDs = make(map[string]time.Time)
	}
	n.seenTxIDs[id] = time.Now()
	n.pruneSeenLocked()
}

func (n *P2PNode) markBlockSeen(hash string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seenBlockHashes == nil {
		n.seenBlockHashes = make(map[string]time.Time)
	}
	n.seenBlockHashes[hash] = time.Now()
	n.pruneSeenLocked()
}

func (n *P2PNode) recordBlockHash(hash string) {
	n.state.RLock()
	height := len(n.state.Chain)
	n.state.RUnlock()

	n.mu.Lock()
	if n.knownBlockHashes == nil {
		n.knownBlockHashes = make(map[string]struct{})
	}
	n.knownBlockHashes[hash] = struct{}{}
	if height > n.indexedHeight {
		n.indexedHeight = height
	}
	n.mu.Unlock()
	n.markBlockSeen(hash)
}

func (n *P2PNode) pruneSeenLocked() {
	for id, ts := range n.seenTxIDs {
		if time.Since(ts) > seenCacheTTL {
			delete(n.seenTxIDs, id)
		}
	}
	for h, ts := range n.seenBlockHashes {
		if time.Since(ts) > seenCacheTTL {
			delete(n.seenBlockHashes, h)
		}
	}
	if len(n.seenTxIDs) > seenCacheLimit {
		n.trimMap(n.seenTxIDs, len(n.seenTxIDs)-seenCacheLimit)
	}
	if len(n.seenBlockHashes) > seenCacheLimit {
		n.trimMap(n.seenBlockHashes, len(n.seenBlockHashes)-seenCacheLimit)
	}
}

func (n *P2PNode) trimMap(m map[string]time.Time, excess int) {
	if excess <= 0 || len(m) == 0 {
		return
	}
	if excess == 1 {
		var oldestKey string
		var oldestTS time.Time
		first := true
		for k, ts := range m {
			if first || ts.Before(oldestTS) {
				oldestKey = k
				oldestTS = ts
				first = false
			}
		}
		delete(m, oldestKey)
		return
	}
	type kv struct {
		key string
		ts  time.Time
	}
	entries := make([]kv, 0, len(m))
	for k, ts := range m {
		entries = append(entries, kv{key: k, ts: ts})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ts.Before(entries[j].ts)
	})
	if excess > len(entries) {
		excess = len(entries)
	}
	for i := 0; i < excess; i++ {
		delete(m, entries[i].key)
	}
}

func (n *P2PNode) refreshKnownBlocks() {
	if n.state == nil {
		return
	}
	n.state.RLock()
	start := n.indexedHeight
	if start < 0 {
		start = 0
	}
	if start > len(n.state.Chain) {
		start = len(n.state.Chain)
	}
	if start == len(n.state.Chain) {
		n.state.RUnlock()
		return
	}
	chainCopy := make([]Block, len(n.state.Chain)-start)
	copy(chainCopy, n.state.Chain[start:])
	n.state.RUnlock()

	now := time.Now()
	n.mu.Lock()
	for _, blk := range chainCopy {
		if blk.Hash == "" {
			continue
		}
		n.knownBlockHashes[blk.Hash] = struct{}{}
		n.seenBlockHashes[blk.Hash] = now
	}
	n.indexedHeight += len(chainCopy)
	n.mu.Unlock()
}

func (n *P2PNode) chainHasBlock(hash string) bool {
	n.refreshKnownBlocks()
	n.mu.Lock()
	_, ok := n.knownBlockHashes[hash]
	n.mu.Unlock()
	return ok
}
