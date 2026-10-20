package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEVMReaderExactAmountsAndFinality(t *testing.T) {
	ctx := context.Background()
	badChain := false
	badToken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     uint64            `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid RPC")
		}
		var result any
		switch q.Method {
		case "eth_chainId":
			result = "0x38"
			if badChain {
				result = "0x1"
			}
		case "eth_blockNumber":
			result = "0x100"
		case "eth_getBlockByNumber":
			result = map[string]string{"number": "0xf0"}
		case "eth_getLogs":
			var filter struct {
				From      string   `json:"fromBlock"`
				To        string   `json:"toBlock"`
				Addresses []string `json:"address"`
			}
			_ = json.Unmarshal(q.Params[0], &filter)
			if filter.From != "0x80" || filter.To != "0x81" || len(filter.Addresses) != 2 {
				t.Error("wrong filter", filter)
			}
			amount := new(big.Int).Mul(big.NewInt(60001234), new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil))
			contract := "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d"
			if badToken {
				contract = "0x" + strings.Repeat("1", 40)
			}
			log := map[string]any{"address": contract, "topics": []string{transferTopic, "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + strings.Repeat("1", 40)}, "data": fmt.Sprintf("0x%064x", amount), "blockNumber": "0x80", "transactionHash": "0x" + strings.Repeat("a", 64), "blockHash": "0x" + strings.Repeat("b", 64), "logIndex": "0x0"}
			result = []any{log}
		default:
			t.Errorf("unknown RPC %s", q.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	defer server.Close()
	reader := NewEVMReader()
	reader.Endpoints = map[string]string{"bsc": server.URL}
	head, err := reader.Head(ctx, "bsc")
	if err != nil || head.Latest != 256 || head.Confirmed != 226 {
		t.Fatal(head, err)
	}
	transfers, err := reader.Transfers(ctx, "bsc", "0x"+strings.Repeat("1", 40), 128, 129)
	if err != nil || len(transfers) != 1 || transfers[0].Amount != "60.001234" {
		t.Fatal(transfers, err)
	}
	badToken = true
	if _, err = reader.Transfers(ctx, "bsc", "0x"+strings.Repeat("1", 40), 128, 129); err == nil {
		t.Fatal("lookalike token accepted")
	}
	badChain = true
	if _, err = reader.Head(ctx, "bsc"); err == nil {
		t.Fatal("wrong chain accepted")
	}
}
func TestStablecoinConfigurationValidation(t *testing.T) {
	for _, address := range []string{"", "0x" + strings.Repeat("0", 40), "0x" + strings.Repeat("z", 40)} {
		if StablecoinAddress(address) {
			t.Fatal("invalid address accepted", address)
		}
	}
	for _, n := range StablecoinNetworks() {
		for _, token := range n.Tokens {
			if !StablecoinAddress(token.Contract) || token.Decimals < 6 {
				t.Fatal("invalid catalog", n, token)
			}
		}
	}
	if _, _, err := StablecoinAsset("ethereum", "DAI"); err == nil {
		t.Fatal("unknown token accepted")
	}
}
