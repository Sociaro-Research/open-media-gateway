package providers

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"time"
)

var ErrBillingUnconfigured = errors.New("add billing access keys in Providers to retrieve provider charges")

func (m *Manager) FetchBills(ctx context.Context, id, month string) ([]core.BillRecord, string, error) {
	record, err := m.loadRecord(ctx, id)
	if err != nil {
		return nil, "", err
	}
	credentials, err := m.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return nil, "", err
	}
	credentials = m.withDefaults(id, credentials)
	if revision := core.ProviderRevision(ctx); revision != "" {
		d, _ := m.findSpec(id)
		if connectionIdentity(id, d, credentials) != revision {
			revisions, ok := m.repo.(providerconfig.Revisions)
			if !ok {
				return nil, "", core.ErrProviderChanged
			}
			raw, e := revisions.GetRevision(ctx, id, revision)
			if e != nil {
				return nil, "", e
			}
			historical, e := m.decryptCredentials(raw)
			if e != nil {
				return nil, "", e
			}
			// Current billing credentials may be added after a task was accepted, but
			// only reuse them when the inference account and endpoint still match.
			if historical.APIKey == credentials.APIKey && historical.BaseURL == credentials.BaseURL {
				historical.BillingAccessKeyID = credentials.BillingAccessKeyID
				historical.BillingAccessKeySecret = credentials.BillingAccessKeySecret
			}
			credentials = historical
		}
	}
	d, ok := m.findSpec(id)
	if !ok || d.Bills == nil {
		return nil, "", ErrUnsupportedProvider
	}
	if configured, _ := d.GroupState(credentials, "billing"); !configured {
		return nil, "", ErrBillingUnconfigured
	}
	identity := connectionIdentity(id, d, credentials)

	raw, err := d.Bills(ctx, credentials, month, m.client)
	if err != nil {
		return nil, identity, err
	}
	result := make([]core.BillRecord, 0, len(raw))
	seen := map[string]bool{}
	for _, row := range raw {
		bill, err := d.NormalizeBill(month, row)
		if err != nil {
			return nil, identity, err
		}
		if seen[bill.ID] {
			return nil, identity, errors.New("billing response contains duplicate detail identities")
		}
		seen[bill.ID] = true
		result = append(result, bill)
	}
	return result, identity, nil
}

const BillingRefreshInterval = 5 * time.Minute

func (m *Manager) SupportsBills(id string) bool { d, ok := m.findSpec(id); return ok && d.Bills != nil }

func (m *Manager) FetchBillsForRevision(ctx context.Context, id, month, revision string) ([]core.BillRecord, string, error) {
	return m.FetchBills(core.WithProviderRevision(ctx, revision), id, month)
}
