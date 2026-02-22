package core

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// P2PNode handles peer-to-peer connectivity over raw TCP.
type P2PNode struct {
	ListenAddr string
	Peers      []string
	state      *ChainState
	dataDir    string
}

// P2PMessage is the wire format used between peers.
type P2PMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// VerifyBlockRequest is sent to peers for PoVS validation.
type VerifyBlockRequest struct {
	Block Block `json:"block"`
}

// VerifyBlockResponse returns a peer's validation vote.
type VerifyBlockResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// NewP2PNode creates a new P2PNode.
func NewP2PNode(listenAddr string, peers []string, state *ChainState, dataDir string) *P2PNode {
	return &P2PNode{
		ListenAddr: listenAddr,
		Peers:      peers,
		state:      state,
		dataDir:    dataDir,
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
		if p != "" {
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
	helloPayload, _ := json.Marshal(map[string]interface{}{
		"node":         "CoCaX-Core",
		"chain_id":     ChainID,
		"chain_length": chainLen,
	})
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
		err := VerifyBlock(n.state, &req.Block)
		if err != nil {
			log.Printf("[P2P] verify_block failed: %v", err)
		}
		resp := P2PMessage{Type: "verify_block_response"}
		body := VerifyBlockResponse{Accepted: err == nil}
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
	replyPayload, _ := json.Marshal(map[string]interface{}{
		"node":         "CoCaX-Core",
		"chain_id":     ChainID,
		"chain_length": chainLen,
	})
	reply.Payload = replyPayload
	if err := enc.Encode(reply); err != nil {
		log.Printf("[P2P] Failed to send hello to %s: %v", addr, err)
		return
	}

	fmt.Printf("[P2P] Handshake complete with %s (peer msg type: %s)\n", addr, msg.Type)
}
