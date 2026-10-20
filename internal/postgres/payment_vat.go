package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func readVATSettings(ctx context.Context, q rowQuerier) (payments.VATSettings, error) {
	var s payments.VATSettings
	var data []byte
	var version int64
	err := q.QueryRowContext(ctx, `SELECT data,version FROM payment_vat_settings WHERE id=TRUE FOR SHARE`).Scan(&data, &version)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	s.Version = version
	if s.Rules == nil {
		s.Rules = []payments.VATRule{}
	}
	return s, nil
}
func (r *SystemRepository) VATSettings(ctx context.Context) (payments.VATSettings, error) {
	return readVATSettings(ctx, r.db)
}
func (r *SystemRepository) SaveVATSettings(ctx context.Context, actor string, s payments.VATSettings) (payments.VATSettings, error) {
	if err := s.Validate(); err != nil {
		if errors.Is(err, payments.ErrSellerVATNumber) {
			return s, err
		}
		return s, payments.ErrVATSettings
	}
	if s.Seller.VATRegistered {
		s.Seller.VATNumber = payments.NormalizeVATNumber(s.Seller.VATNumber)
	}
	logo, err := payments.NormalizeInvoiceLogo(s.Seller.InvoiceLogo)
	if err != nil {
		return s, err
	}
	s.Seller.InvoiceLogo = logo
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return s, err
	}
	if _, err = systemActor(ctx, tx, actor); err != nil {
		return s, err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE payment_vat_settings SET data=$1,version=version+1,updated_at=NOW() WHERE id=TRUE AND version=$2`, data, s.Version)
	if err != nil {
		return s, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return s, err
	}
	if count != 1 {
		return s, system.ErrConflict
	}
	s.Version++
	return s, tx.Commit()
}
func readVATVerification(ctx context.Context, q rowQuerier, org string) (payments.VATVerification, error) {
	var v payments.VATVerification
	var data []byte
	var version int64
	err := q.QueryRowContext(ctx, `SELECT data,version FROM organization_vat_verifications WHERE organization_id=$1`, org).Scan(&data, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(data, &v)
	v.Version = version
	return v, err
}
func (r *SystemRepository) VATVerification(ctx context.Context, org string) (payments.VATVerification, error) {
	return readVATVerification(ctx, r.db, org)
}
func (r *SystemRepository) SaveVATVerification(ctx context.Context, actor, org string, v payments.VATVerification) (payments.VATVerification, error) {
	now := time.Now().UTC()
	v.Number = payments.NormalizeVATNumber(v.Number)
	if v.Verified {
		if !payments.CountryCode(v.Country) || !payments.VATCountryMatch(v.Country, v.Number) || strings.TrimSpace(v.BusinessName) == "" || len(v.BusinessName) > 200 || strings.TrimSpace(v.Reference) == "" || len(v.Reference) > 1000 {
			return v, payments.ErrVATVerification
		}
		if v.ExpiresAt.IsZero() {
			v.ExpiresAt = now.Add(90 * 24 * time.Hour)
		}
		if !v.ExpiresAt.After(now) || v.ExpiresAt.After(now.Add(366*24*time.Hour)) {
			return v, payments.ErrVATVerification
		}
	}
	v.Source = "manual"
	v.VerifiedAt = now
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return v, err
	}
	if _, err = systemActor(ctx, tx, actor); err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, org); err != nil {
		return v, err
	}
	before, err := readVATVerification(ctx, tx, org)
	if err != nil {
		return v, err
	}
	if before.Version != v.Version {
		return v, system.ErrConflict
	}
	v.Version++
	data, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_vat_verifications(organization_id,data,version) VALUES($1,$2,$3) ON CONFLICT(organization_id) DO UPDATE SET data=EXCLUDED.data,version=EXCLUDED.version,updated_at=NOW()`, org, data, v.Version)
	if err != nil {
		return v, paymentDBError(err)
	}
	return v, tx.Commit()
}

var _ payments.VATRepository = (*SystemRepository)(nil)
