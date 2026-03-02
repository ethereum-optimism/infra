package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	url10000 = "http://127.0.0.1:10000"
	url10001 = "http://127.0.0.1:10001"
)

type jsonRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	ID int `json:"id"`
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func doRPC(url, method string, params []interface{}) ([]byte, error) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func doRPCWithLatency(url, method string, params []interface{}) ([]byte, time.Duration, error) {
	start := time.Now()
	data, err := doRPC(url, method, params)
	elapsed := time.Since(start)
	return data, elapsed, err
}

func getBlockNumber(url string) (string, error) {
	data, err := doRPC(url, "eth_blockNumber", nil)
	if err != nil {
		return "", err
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return "", err
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}

	var result string
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return "", err
	}
	return result, nil
}

func main() {
	var bnFlag = flag.String("bn", "", "block number (hex, e.g. 0x12345); if empty, fetches latest each iteration")
	flag.Parse()

	for {
		var blockArg string
		if *bnFlag != "" {
			blockArg = *bnFlag
		} else {
			bn, err := getBlockNumber(url10000)
			if err != nil {
				fmt.Printf("Error getting block number: %v\n", err)
				time.Sleep(1 * time.Second)
				continue
			}
			blockArg = bn
		}

		params := []interface{}{blockArg}

		_, t10000, err10000 := doRPCWithLatency(url10000, "eth_getBlockReceipts", params)
		_, t10001, err10001 := doRPCWithLatency(url10001, "eth_getBlockReceipts", params)

		if err10000 != nil {
			fmt.Printf("Block: %s | Port 10000 error: %v\n", blockArg, err10000)
		}
		if err10001 != nil {
			fmt.Printf("Block: %s | Port 10001 error: %v\n", blockArg, err10001)
		}
		if err10000 != nil || err10001 != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		ms10000 := t10000.Milliseconds()
		ms10001 := t10001.Milliseconds()

		var ratio string
		if ms10000 > 0 {
			improvement := float64(ms10000-ms10001) / float64(ms10000) * 100
			ratio = fmt.Sprintf("%.1f%%", improvement)
		} else {
			ratio = "N/A"
		}

		fmt.Printf("Block: %s | Port 10000: %dms | Port 10001: %dms | 优化比例: %s\n",
			blockArg, ms10000, ms10001, ratio)

		time.Sleep(1 * time.Second)
	}
}
