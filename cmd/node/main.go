package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"cocax-core/core"
	"cocax-core/rpc"
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
	allowPlaceholder := flag.Bool("allow-placeholder", false, "Allow running with the placeholder founder address (development only)")
	flag.Parse()

	// -genaddr: generate a fresh key pair, print the address, then exit.
	if *genaddr {
		printGeneratedAddress()
		return
	}

	founderAddr := *founder
	if founderAddr == "" {
		founderAddr = core.FounderAddress
	}
	minerAddr := *miner
	if minerAddr == "" {
		minerAddr = founderAddr
	}
	if founderAddr == core.FounderAddress && !*allowPlaceholder {
		log.Fatalf("[%s] Placeholder founder address is not allowed without -allow-placeholder; set -founder to your wallet address.", core.CoinName)
	}

	log.Printf("[%s] Starting CoCaX-Core node...", core.CoinName)
	log.Printf("[%s] Data directory: %s", core.CoinName, *dataDir)
	log.Printf("[%s] Chain ID: %d", core.CoinName, core.ChainID)
	if founderAddr == core.FounderAddress {
		log.Printf("[%s] Warning: using placeholder founder address (%s); set -founder to your wallet address for production use.", core.CoinName, founderAddr)
	} else {
		log.Printf("[%s] Founder address: %s", core.CoinName, founderAddr)
	}

	state, err := core.LoadState(*dataDir, founderAddr)
	if err != nil {
		log.Fatalf("[%s] Failed to load state: %v", core.CoinName, err)
	}
	log.Printf("[%s] Chain loaded: %d blocks, minted supply: %.2f %s",
		core.CoinName, len(state.Chain), state.MintedSupply, core.Ticker)

	// Start P2P listener.
	peerList := []string{}
	if *peers != "" {
		peerList = strings.Split(*peers, ",")
	}
	p2p := core.NewP2PNode(*addr, peerList, state, *dataDir)
	p2p.Start()

	// Mine a block on startup if requested.
	if *mine {
		block, err := core.MineBlock(state, minerAddr, *dataDir)
		if err != nil {
			log.Printf("[%s] Mine error: %v", core.CoinName, err)
		} else {
			log.Printf("[%s] Mined block #%d hash=%s reward=%.4f %s",
				core.CoinName, block.Index, block.Hash[:16], block.Reward, core.Ticker)
		}
	}

	// Start HTTP API (RPC).
	api := rpc.NewServer(state, *dataDir, minerAddr)
	log.Printf("[%s] HTTP API listening on  http://%s", core.CoinName, *apiAddr)
	log.Printf("[%s] Blocks endpoint:        http://%s/blocks", core.CoinName, *apiAddr)
	log.Printf("[%s] Balance endpoint:       http://%s/balance/<address>", core.CoinName, *apiAddr)
	log.Printf("[%s] Run command: go run ./cmd/node -addr 0.0.0.0:9000 -api 0.0.0.0:8080 -mine -data ./data", core.CoinName)

	if err := http.ListenAndServe(*apiAddr, api.Router()); err != nil {
		log.Fatalf("[%s] HTTP server error: %v", core.CoinName, err)
	}
}

// printGeneratedAddress generates a fresh ECDSA P-256 key pair, prints the
// private key (hex), public key coordinates (hex), and derived CoX address,
// then exits.  The address can be used as the -founder flag on first run.
func printGeneratedAddress() {
	priv, err := core.GenerateKeyPair()
	if err != nil {
		log.Fatalf("key generation failed: %v", err)
	}
	addr := core.DeriveAddress(&priv.PublicKey)
	privHex := hex.EncodeToString(priv.D.Bytes())
	xHex := hex.EncodeToString(core.PadTo32(priv.PublicKey.X.Bytes()))
	yHex := hex.EncodeToString(core.PadTo32(priv.PublicKey.Y.Bytes()))

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
	fmt.Println("  1. Start the node API (RPC) separately, then use the wallet client to connect.")
	fmt.Printf("       go run ./cmd/node -api 0.0.0.0:8080 -data ./data-tmp\n")
	fmt.Println("  -- OR use the address printed above directly --")
	fmt.Println("  2. Start the production node with your address as the founder:")
	fmt.Printf("       go run ./cmd/node -founder %s -data ./data\n", addr)
	fmt.Println("  3. Your founder address will receive 3,300,000 CoX on genesis.")
	fmt.Println()
	fmt.Println("  SECURITY: Keep your private key and mnemonic phrase offline!")
	fmt.Println("================================================================")
	fmt.Println()
}
