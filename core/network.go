package core

import (
	"encoding/json"
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

	mu              sync.Mutex
	seenTxIDs       map[string]time.Time
	seenBlockHashes map[string]time.Time
}

// P2PMessage is the wire format used between peers.
type P2PMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
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

// NewP2PNode creates a new P2PNode.
func NewP2PNode(listenAddr string, peers []string, state *ChainState, dataDir string) *P2PNode {
	return &P2PNode{
		ListenAddr:      listenAddr,
		Peers:           peers,
		state:           state,
		dataDir:         dataDir,
		seenTxIDs:       make(map[string]time.Time),
		seenBlockHashes: make(map[string]time.Time),
	}
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
	var msg P2PMessage
	if err := dec.Decode(&msg); err != nil {
		return
	}

	switch msg.Type {
	case "hello":
		log.Printf("[P2P] Peer connected from %s", conn.RemoteAddr())
	case "get_blocks":
		n.state.mu.RLock()
		blocks := n.state.Chain
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
		if n.hasSeenBlock(blk.Hash) || n.chainHasBlock(blk.Hash) {
			return
		}
		if err := AddBlock(n.state, &blk, n.dataDir); err != nil {
			log.Printf("[P2P] Rejected block %s: %v", blk.Hash, err)
			return
		}
		n.BroadcastBlock(&blk)
	default:
		log.Printf("[P2P] Unknown message type: %s", msg.Type)
	}
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
	if n.hasSeenBlock(block.Hash) {
		return
	}
	n.markBlockSeen(block.Hash)
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

func (n *P2PNode) pruneSeenLocked() {
	now := time.Now()
	for id, ts := range n.seenTxIDs {
		if now.Sub(ts) > seenCacheTTL {
			delete(n.seenTxIDs, id)
		}
	}
	for h, ts := range n.seenBlockHashes {
		if now.Sub(ts) > seenCacheTTL {
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

func (n *P2PNode) chainHasBlock(hash string) bool {
	n.state.RLock()
	defer n.state.RUnlock()
	for _, blk := range n.state.Chain {
		if blk.Hash == hash {
			n.markBlockSeen(hash)
			return true
		}
	}
	return false
}
