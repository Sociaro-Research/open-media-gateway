package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// EVMReader uses fixed, public HTTPS endpoints; no signing key is ever loaded.
// Operators can substitute a trusted reader at startup without exposing RPC
// credentials or arbitrary request destinations to organization users.
type EVMReader struct {
	Client    *http.Client
	Endpoints map[string]string
	sequence  atomic.Uint64
}

func NewEVMReader() *EVMReader {
	return &EVMReader{Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (r *EVMReader) rpc(ctx context.Context, network, method string, params any, out any) error {
	n, _, err := StablecoinAsset(network, "USDC")
	if err != nil {
		return err
	}
	endpoint := n.RPC
	if r.Endpoints[network] != "" {
		endpoint = r.Endpoints[network]
	}
	id := r.sequence.Add(1)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sociaro-Gateway-Stablecoins/1.0")
	resp, err := r.Client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return ErrUnavailable
	}
	var result struct {
		ID     uint64          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &result) != nil || result.ID != id || (len(result.Error) > 0 && string(result.Error) != "null") || len(result.Result) == 0 || string(result.Result) == "null" {
		return ErrUnavailable
	}
	if json.Unmarshal(result.Result, out) != nil {
		return ErrUnavailable
	}
	return nil
}
func hexBlock(s string) (uint64, error) {
	if !strings.HasPrefix(s, "0x") || len(s) <= 2 {
		return 0, ErrUnavailable
	}
	v, e := strconv.ParseUint(s[2:], 16, 64)
	if e != nil {
		return 0, ErrUnavailable
	}
	return v, nil
}
func (r *EVMReader) Head(ctx context.Context, network string) (ChainHead, error) {
	n, _, err := StablecoinAsset(network, "USDC")
	if err != nil {
		return ChainHead{}, err
	}
	var chain, latest string
	if err = r.rpc(ctx, network, "eth_chainId", []any{}, &chain); err != nil {
		return ChainHead{}, err
	}
	id, err := hexBlock(chain)
	if err != nil || id != n.ChainID {
		return ChainHead{}, ErrConflict
	}
	if err = r.rpc(ctx, network, "eth_blockNumber", []any{}, &latest); err != nil {
		return ChainHead{}, err
	}
	height, err := hexBlock(latest)
	if err != nil || height < n.Confirmations {
		return ChainHead{}, ErrUnavailable
	}
	var finalized struct {
		Number string `json:"number"`
	}
	if err = r.rpc(ctx, network, "eth_getBlockByNumber", []any{"finalized", false}, &finalized); err != nil {
		return ChainHead{}, err
	}
	final, err := hexBlock(finalized.Number)
	if err != nil || final > height {
		return ChainHead{}, ErrUnavailable
	}
	return ChainHead{height, min(height-n.Confirmations, final)}, nil
}

const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

func (r *EVMReader) Transfers(ctx context.Context, network, address string, from, to uint64) ([]StablecoinTransfer, error) {
	n, _, err := StablecoinAsset(network, "USDC")
	if err != nil || !StablecoinAddress(address) || from > to || to-from > 999 {
		return nil, ErrInvalid
	}
	contracts := []string{}
	assets := map[string]StablecoinToken{}
	for _, t := range n.Tokens {
		contracts = append(contracts, t.Contract)
		assets[t.Contract] = t
	}
	var logs []struct {
		Address   string   `json:"address"`
		Topics    []string `json:"topics"`
		Data      string   `json:"data"`
		Block     string   `json:"blockNumber"`
		Hash      string   `json:"transactionHash"`
		BlockHash string   `json:"blockHash"`
		Index     string   `json:"logIndex"`
		Removed   bool     `json:"removed"`
	}
	topic := "0x" + strings.Repeat("0", 24) + strings.ToLower(address[2:])
	filter := map[string]any{"address": contracts, "fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to), "topics": []any{transferTopic, nil, topic}}
	if err = r.rpc(ctx, network, "eth_getLogs", []any{filter}, &logs); err != nil {
		return nil, err
	}
	out := []StablecoinTransfer{}
	for _, l := range logs {
		token, ok := assets[strings.ToLower(l.Address)]
		if !ok || l.Removed || len(l.Topics) != 3 || l.Topics[0] != transferTopic || !strings.EqualFold(l.Topics[2], topic) {
			return nil, ErrConflict
		}
		block, e := hexBlock(l.Block)
		if e != nil || block < from || block > to {
			return nil, ErrConflict
		}
		index, e := hexBlock(l.Index)
		if e != nil {
			return nil, e
		}
		if !hashValue(l.Hash) || !hashValue(l.BlockHash) || len(l.Data) != 66 || !strings.HasPrefix(l.Data, "0x") {
			return nil, ErrConflict
		}
		amount, ok := new(big.Int).SetString(l.Data[2:], 16)
		if !ok {
			return nil, ErrConflict
		}
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(token.Decimals-6)), nil)
		micros, remainder := new(big.Int), new(big.Int)
		micros.QuoRem(amount, divisor, remainder)
		// Nonrepresentable amounts cannot match a six-decimal request.
		if remainder.Sign() != 0 || !micros.IsInt64() || micros.Sign() <= 0 {
			continue
		}
		out = append(out, StablecoinTransfer{token.Contract, strings.ToLower(address), StablecoinAmount(micros.Int64()), strings.ToLower(l.Hash), strings.ToLower(l.BlockHash), block, index})
	}
	return out, nil
}
func hashValue(s string) bool {
	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return false
	}
	_, ok := new(big.Int).SetString(s[2:], 16)
	return ok
}
