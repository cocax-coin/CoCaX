package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:9000", "P2P listen address")
	apiAddr := flag.String("api", "0.0.0.0:8080", "HTTP API listen address")
	peers := flag.String("peers", "", "Comma-separated list of peer addresses")
	miner := flag.String("miner", "", "Miner address for block rewards (default: founder address)")
	mine := flag.Bool("mine", false, "Mine one block immediately on startup")
	dataDir := flag.String("data", "./data", "Data directory for persistence (blocks.json, state.json)")
	founder := flag.String("founder", "", "Your CoX address to receive the 3.3M founder allocation on first run")
	genaddr := flag.Bool("genaddr", false, "Generate a new CoX address and exit (use with wallet to get founder address)")
	flag.Parse()

	// -genaddr: generate a fresh key pair, print the address, then exit.
	if *genaddr {
		printGeneratedAddress()
		return
	}

	founderAddr := *founder
	if founderAddr == "" {
		founderAddr = FounderAddress
	}
	minerAddr := *miner
	if minerAddr == "" {
		minerAddr = founderAddr
	}

	log.Printf("[%s] Starting CoCaX-Core node...", CoinName)
	log.Printf("[%s] Data directory: %s", CoinName, *dataDir)
	if founderAddr != FounderAddress {
		log.Printf("[%s] Founder address: %s", CoinName, founderAddr)
	}

	state, err := LoadState(*dataDir, founderAddr)
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
		block, err := MineBlock(state, minerAddr, *dataDir)
		if err != nil {
			log.Printf("[%s] Mine error: %v", CoinName, err)
		} else {
			log.Printf("[%s] Mined block #%d hash=%s reward=%.4f %s",
				CoinName, block.Index, block.Hash[:16], block.Reward, Ticker)
		}
	}

	// Start HTTP API.
	api := NewAPIServer(state, *dataDir, minerAddr)
	log.Printf("[%s] HTTP API listening on  http://%s", CoinName, *apiAddr)
	log.Printf("[%s] Wallet UI:              http://%s/", CoinName, *apiAddr)
	log.Printf("[%s] Blocks endpoint:        http://%s/blocks", CoinName, *apiAddr)
	log.Printf("[%s] Balance endpoint:       http://%s/balance/<address>", CoinName, *apiAddr)
	log.Printf("[%s] Run command: go run . -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data", CoinName)

	if err := http.ListenAndServe(*apiAddr, api.Router()); err != nil {
		log.Fatalf("[%s] HTTP server error: %v", CoinName, err)
	}
}

// printGeneratedAddress generates a fresh ECDSA P-256 key pair, prints the
// private key (hex), public key coordinates (hex), and derived CoX address,
// then exits.  The address can be used as the -founder flag on first run.
func printGeneratedAddress() {
	priv, err := GenerateKeyPair()
	if err != nil {
		log.Fatalf("key generation failed: %v", err)
	}
	addr := DeriveAddress(&priv.PublicKey)
	privHex := hex.EncodeToString(priv.D.Bytes())
	xHex := hex.EncodeToString(padTo32(priv.PublicKey.X.Bytes()))
	yHex := hex.EncodeToString(padTo32(priv.PublicKey.Y.Bytes()))

	fmt.Println()
	fmt.Println("================================================================")
	fmt.Println("  CoCaX Address Generator  |  CoXaNa Wallet Compatible")
	fmt.Println("================================================================")
	fmt.Printf("  CoX Address  : %s\n", addr)
	fmt.Printf("  Private Key  : %s\n", privHex)
	fmt.Printf("  Public Key X : %s\n", xHex)
	fmt.Printf("  Public Key Y : %s\n", yHex)
	fmt.Println()
	fmt.Println("  HOW TO USE:")
	fmt.Println("  1. Open the wallet UI by starting the node in a separate terminal:")
	fmt.Println("       go run . -api 0.0.0.0:8080 -data ./data-tmp")
	fmt.Println("     Then visit http://localhost:8080/ to create a mnemonic wallet")
	fmt.Println("     and obtain your CoX address.")
	fmt.Println("  -- OR use the address printed above directly --")
	fmt.Println("  2. Start the production node with your address as the founder:")
	fmt.Printf("       go run . -founder %s -data ./data\n", addr)
	fmt.Println("  3. Your founder address will receive 3,300,000 CoX on genesis.")
	fmt.Println()
	fmt.Println("  SECURITY: Keep your private key and mnemonic phrase offline!")
	fmt.Println("================================================================")
	fmt.Println()
}
