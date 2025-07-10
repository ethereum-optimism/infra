package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/ethereum-optimism/optimism/op-chain-ops/devkeys"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

var bigZero = new(big.Int)

type Config struct {
	EthRpcUrl     string
	FaucetUrl     string
	MinBalance    eth.ETH
	DaemonBinPath string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig() (*Config, error) {
	config := &Config{
		EthRpcUrl:     "http://localhost:8545",
		FaucetUrl:     "http://localhost:8546",
		MinBalance:    eth.OneEther,
		DaemonBinPath: "./spamoor-daemon",
	}

	if ethRpcUrl, exists := os.LookupEnv("ETH_RPC_URL"); exists {
		config.EthRpcUrl = ethRpcUrl
	}

	if faucetUrl, exists := os.LookupEnv("FAUCET_URL"); exists {
		config.FaucetUrl = faucetUrl
	}

	if minBalanceStr, exists := os.LookupEnv("MIN_BALANCE"); exists {
		minBalance, ok := new(big.Int).SetString(minBalanceStr, 10)
		if !ok {
			return nil, fmt.Errorf("invalid min balance: %s", minBalanceStr)
		}
		if minBalance.Cmp(bigZero) == -1 {
			return nil, fmt.Errorf("min balance must be positive: %s", minBalanceStr)
		}
		config.MinBalance = eth.WeiBig(minBalance)
	}

	if daemonBinPath, exists := os.LookupEnv("DAEMON_BIN_PATH"); exists {
		config.DaemonBinPath = daemonBinPath
	}

	return config, nil
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	config, err := loadConfig()
	if err != nil {
		return err
	}

	// Acceptance tests tend to use keys with small indices. To avoid spending from an account also
	// used by a test, use a large index.
	key := devkeys.UserKey(12345)
	hd, err := devkeys.NewMnemonicDevKeys(devkeys.TestMnemonic)
	if err != nil {
		return fmt.Errorf("new mnemonic: %v", err)
	}
	address, err := hd.Address(key)
	if err != nil {
		return fmt.Errorf("get address: %v", err)
	}
	privKey, err := hd.Secret(key)
	if err != nil {
		return fmt.Errorf("get private key: %v", err)
	}

	client, err := ethclient.DialContext(ctx, config.EthRpcUrl)
	if err != nil {
		return fmt.Errorf("dial EL: %v", err)
	}
	balanceBig, err := client.BalanceAt(ctx, address, nil)
	if err != nil {
		return fmt.Errorf("get balance: %v", err)
	}
	balance := eth.WeiBig(balanceBig)

	if balance.Lt(config.MinBalance) {
		// Request funds from faucet.
		client, err := rpc.DialContext(ctx, config.FaucetUrl)
		if err != nil {
			return fmt.Errorf("dial faucet: %v", err)
		}
		missing := config.MinBalance.Sub(balance)
		if err := client.CallContext(ctx, nil, "faucet_requestETH", address, missing); err != nil {
			return fmt.Errorf("request ETH from faucet: %v", err)
		}
	}

	privKeyHex := "0x" + common.Bytes2Hex(crypto.FromECDSA(privKey))
	cmd := exec.CommandContext(ctx, config.DaemonBinPath, append([]string{"--privkey", privKeyHex}, os.Args[1:]...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("daemon: %v", err)
	}

	return nil
}
