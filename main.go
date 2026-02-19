package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:9000", "P2P listen address")
	apiAddr := flag.String("api", "0.0.0.0:8080", "HTTP API listen address")
	peers := flag.String("peers", "", "Comma-separated list of peer addresses")
	miner := flag.String("miner", FounderAddress, "Miner address for block rewards")
	mine := flag.Bool("mine", false, "Mine one block immediately on startup")
	dataDir := flag.String("data", "./data", "Data directory for persistence (blocks.json, state.json)")
	flag.Parse()

	log.Printf("[%s] Starting CoCaX-Core node...", CoinName)
	log.Printf("[%s] Data directory: %s", CoinName, *dataDir)

	state, err := LoadState(*dataDir)
	if err != nil {
		log.Fatalf("[%s] Failed to load state: %v", CoinName, err)
	}
	log.Printf("[%s] Chain loaded: %d blocks, minted supply: %.2f %s",
		CoinName, len(state.Chain), state.MintedSupply, Ticker)

	// Start P2P listener.
	peerList := []string{}
	if *peers != "" {
		peerList = strings.Split(*peers, ",")
	}
	p2p := NewP2PNode(*addr, peerList, state, *dataDir)
	p2p.Start()

	// Mine a block on startup if requested.
	if *mine {
		block, err := MineBlock(state, *miner, *dataDir)
		if err != nil {
			log.Printf("[%s] Mine error: %v", CoinName, err)
		} else {
			log.Printf("[%s] Mined block #%d hash=%s reward=%.4f %s",
				CoinName, block.Index, block.Hash[:16], block.Reward, Ticker)
		}
	}

	// Start HTTP API.
	api := NewAPIServer(state, *dataDir, *miner)
	log.Printf("[%s] HTTP API listening on  http://%s", CoinName, *apiAddr)
	log.Printf("[%s] Wallet UI:              http://%s/", CoinName, *apiAddr)
	log.Printf("[%s] Blocks endpoint:        http://%s/blocks", CoinName, *apiAddr)
	log.Printf("[%s] Balance endpoint:       http://%s/balance/<address>", CoinName, *apiAddr)
	log.Printf("[%s] Run command: go run . -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data", CoinName)

	if err := http.ListenAndServe(*apiAddr, api.Router()); err != nil {
		log.Fatalf("[%s] HTTP server error: %v", CoinName, err)
	}
}
