package payments

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/google/uuid"
)

// The catalog deliberately exposes one accepted contract per token/network pair.
// Amounts use six decimal places even where the on-chain token supports eighteen.
var ErrAmountExhausted = fmt.Errorf("no unused payment amounts remain; choose another top-up amount")

type StablecoinNetwork struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	ChainID       uint64            `json:"chain_id"`
	Confirmations uint64            `json:"-"`
	RPC           string            `json:"-"`
	Explorer      string            `json:"-"`
	Tokens        []StablecoinToken `json:"tokens"`
}
type StablecoinToken struct {
	Symbol   string `json:"symbol"`
	Contract string `json:"contract"`
	Decimals int    `json:"decimals"`
}

func StablecoinNetworks() []StablecoinNetwork {
	return []StablecoinNetwork{
		{"ethereum", "Ethereum", 1, 64, "https://ethereum-rpc.publicnode.com", "https://etherscan.io", []StablecoinToken{{"USDT", "0xdac17f958d2ee523a2206206994597c13d831ec7", 6}, {"USDC", "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", 6}}},
		{"bsc", "BNB Smart Chain", 56, 30, "https://bsc-rpc.publicnode.com", "https://bscscan.com", []StablecoinToken{{"USDT", "0x55d398326f99059ff775485246999027b3197955", 18}, {"USDC", "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", 18}}},
		{"polygon", "Polygon", 137, 128, "https://polygon-bor-rpc.publicnode.com", "https://polygonscan.com", []StablecoinToken{{"USDT", "0xc2132d05d31c914a87c6611c10748aeb04b58e8f", 6}, {"USDC", "0x3c499c542cef5e3811e1192ce70d8cc03d5c3359", 6}}},
	}
}
func StablecoinAsset(network, symbol string) (StablecoinNetwork, StablecoinToken, error) {
	for _, n := range StablecoinNetworks() {
		if n.ID == network {
			for _, t := range n.Tokens {
				if t.Symbol == symbol {
					return n, t, nil
				}
			}
		}
	}
	return StablecoinNetwork{}, StablecoinToken{}, ErrInvalid
}
func StablecoinAddress(s string) bool {
	if len(s) != 42 || !strings.HasPrefix(s, "0x") || strings.EqualFold(s, "0x"+strings.Repeat("0", 40)) {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}

type StablecoinWallet struct {
	Network string   `json:"network"`
	Address string   `json:"address"`
	Tokens  []string `json:"tokens"`
}
type StablecoinConfig struct {
	Wallets []StablecoinWallet `json:"wallets"`
}

func (c *StablecoinConfig) Validate() error {
	if c == nil || len(c.Wallets) == 0 || len(c.Wallets) > 3 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for i, w := range c.Wallets {
		if seen[w.Network] || !StablecoinAddress(w.Address) || len(w.Tokens) == 0 || len(w.Tokens) > 2 {
			return ErrInvalid
		}
		seen[w.Network] = true
		tokens := map[string]bool{}
		for _, t := range w.Tokens {
			if _, _, err := StablecoinAsset(w.Network, t); err != nil || tokens[t] {
				return ErrInvalid
			}
			tokens[t] = true
		}
		c.Wallets[i].Address = strings.ToLower(w.Address)
	}
	return nil
}

type StablecoinPayment struct {
	Network         string       `json:"network"`
	Token           string       `json:"token"`
	Contract        string       `json:"contract"`
	Address         string       `json:"address"`
	Amount          string       `json:"amount"`
	FeeMicros       int64        `json:"fee_micros"`
	StartBlock      uint64       `json:"start_block"`
	VAT             *VATSnapshot `json:"vat"`
	TransactionHash string       `json:"transaction_hash,omitempty"`
	LogIndex        uint64       `json:"log_index,omitempty"`
	BlockNumber     uint64       `json:"block_number,omitempty"`
	ExplorerURL     string       `json:"explorer_url,omitempty"`
}

func StablecoinAmount(micros int64) string {
	return fmt.Sprintf("%d.%06d", micros/1000000, micros%1000000)
}

type StablecoinInput struct {
	ProviderID string         `json:"provider_id"`
	Amount     money.USD      `json:"amount_usd"`
	Network    string         `json:"network"`
	Token      string         `json:"token"`
	Billing    BillingProfile `json:"billing"`
}
type ChainHead struct{ Latest, Confirmed uint64 }
type StablecoinTransfer struct {
	Contract, Address, Amount, Hash, BlockHash string
	Block, Index                               uint64
}
type ChainReader interface {
	Head(context.Context, string) (ChainHead, error)
	Transfers(context.Context, string, string, uint64, uint64) ([]StablecoinTransfer, error)
}
type StablecoinRepository interface {
	BeginStablecoin(context.Context, string, string, StablecoinInput, string, *VATCalculator, ChainReader) (Payment, error)
	CancelStablecoin(context.Context, string, string, string) (Payment, error)
	ScanStablecoins(context.Context, ChainReader) error
}

func (m *Manager) saveStablecoin(ctx context.Context, actor, id string, in ProviderUpdate) (ProviderView, error) {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 100 || in.Stablecoin.Validate() != nil {
		return ProviderView{}, ErrInvalid
	}
	if in.Enabled {
		repo, ok := m.Repo.(VATRepository)
		if !ok {
			return ProviderView{}, ErrVATSettings
		}
		settings, err := repo.VATSettings(ctx)
		if err != nil {
			return ProviderView{}, err
		}
		if settings.Ready() != nil {
			return ProviderView{}, ErrVATSettings
		}
	}
	p := Provider{ID: uuid.NewString(), Kind: "stablecoin", Name: strings.TrimSpace(in.Name), Live: true, FeeMode: "service", Enabled: in.Enabled, Version: in.Version, Stablecoin: in.Stablecoin}
	if id != "" {
		old, e := m.Repo.PaymentProvider(ctx, id)
		if e != nil {
			return ProviderView{}, e
		}
		if old.Kind != "stablecoin" {
			return ProviderView{}, ErrConflict
		}
		p.ID = id
	} else if in.Version != 0 {
		return ProviderView{}, ErrConflict
	}
	p, err := m.Repo.SavePaymentProvider(ctx, actor, p)
	return m.view(p), err
}
func (m *Manager) StablecoinCheckout(ctx context.Context, actor, org string, in StablecoinInput, key string) (Payment, error) {
	if _, err := Cents(in.Amount); err != nil || !ValidReference(key) {
		return Payment{}, ErrInvalid
	}
	if !validBilling(in.Billing) {
		return Payment{}, ErrInvalid
	}
	if _, _, err := StablecoinAsset(in.Network, in.Token); err != nil {
		return Payment{}, err
	}
	repo, ok := m.Repo.(StablecoinRepository)
	if !ok || m.Chain == nil {
		return Payment{}, ErrUnavailable
	}
	if m.VAT == nil {
		return Payment{}, ErrVATSettings
	}
	return repo.BeginStablecoin(ctx, actor, org, in, key, m.VAT, m.Chain)
}
func validBilling(b BillingProfile) bool {
	a := b.Address
	if strings.TrimSpace(b.BusinessName) == "" || len(b.BusinessName) > 200 || !CountryCode(a.Country) || strings.TrimSpace(a.Line1) == "" || (strings.TrimSpace(a.City) == "" && strings.TrimSpace(a.PostalCode) == "") || len(b.TaxIDs) > 5 {
		return false
	}
	for _, v := range []string{b.Email, a.Line1, a.Line2, a.City, a.State, a.PostalCode} {
		if len(v) > 300 || strings.ContainsAny(v, "\r\n") {
			return false
		}
	}
	for _, v := range b.TaxIDs {
		if len(v.Type) > 40 || len(v.Value) > 100 || v.Type == "" || v.Value == "" {
			return false
		}
	}
	return true
}
