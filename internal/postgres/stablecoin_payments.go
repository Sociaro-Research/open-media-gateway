package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/google/uuid"
)

func (r *SystemRepository) BeginStablecoin(ctx context.Context, actor, org string, in payments.StablecoinInput, key string, vat *payments.VATCalculator, chain payments.ChainReader) (payments.Payment, error) {
	cents, err := payments.Cents(in.Amount)
	if err != nil || !payments.ValidReference(key) {
		return payments.Payment{}, payments.ErrInvalid
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return payments.Payment{}, err
	}
	defer tx.Rollback()
	if _, err = orgLock(ctx, tx, actor, org); err != nil {
		return payments.Payment{}, err
	}
	old, err := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE organization_id=$1 AND actor_id=$2 AND idempotency_key=$3`, org, actor, key))
	if err == nil {
		if old.ProviderID != in.ProviderID || old.AmountCents != cents || old.Stablecoin == nil || old.Stablecoin.Network != in.Network || old.Stablecoin.Token != in.Token || old.Billing == nil {
			return old, payments.ErrConflict
		}
		a, _ := json.Marshal(payments.BillingProfile{BusinessName: old.Billing.BusinessName, Email: old.Billing.Email, Address: old.Billing.Address, TaxIDs: old.Billing.TaxIDs})
		b, _ := json.Marshal(in.Billing)
		if string(a) != string(b) {
			return old, payments.ErrConflict
		}
		return old, tx.Commit()
	}
	if !errors.Is(err, payments.ErrNotFound) {
		return old, err
	}
	pr, err := scanPaymentProvider(tx.QueryRowContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers WHERE id=$1 FOR SHARE`, in.ProviderID))
	if err != nil {
		return old, err
	}
	if pr.Kind != "stablecoin" || !pr.Enabled || !pr.Live || pr.Stablecoin == nil {
		return old, payments.ErrUnavailable
	}
	n, asset, err := payments.StablecoinAsset(in.Network, in.Token)
	if err != nil {
		return old, err
	}
	address := ""
	for _, w := range pr.Stablecoin.Wallets {
		if w.Network == in.Network {
			for _, t := range w.Tokens {
				if t == in.Token {
					address = w.Address
				}
			}
		}
	}
	if address == "" {
		return old, payments.ErrInvalid
	}
	p := payments.Payment{ID: uuid.NewString(), OrganizationID: org, ProviderID: pr.ID, ActorID: actor, AmountCents: cents, Live: true, FeeMode: "service", TaxMode: "exclusive", Currency: "usd"}
	settings, err := readVATSettings(ctx, tx)
	if err != nil {
		return p, err
	}
	manual, err := readVATVerification(ctx, tx, org)
	if err != nil {
		return p, err
	}
	billing, snapshot, err := vat.Quote(ctx, settings, p, in.Billing, manual)
	if err != nil {
		return p, err
	}
	if _, err = payments.Net(p, payments.Snapshot{Billing: billing}); err != nil {
		return p, err
	}
	head, err := chain.Head(ctx, in.Network)
	if err != nil {
		return p, err
	}
	gross := billing.TotalCents * 10000
	// Serialize amount allocation across organizations and provider revisions.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.Network+":"+asset.Contract+":"+address+":"+payments.StablecoinAmount(gross)); err != nil {
		return p, err
	}
	random, err := rand.Int(rand.Reader, big.NewInt(9999))
	if err != nil {
		return p, err
	}
	fee := int64(0)
	for attempt := int64(0); attempt < 9999; attempt++ {
		candidate := (random.Int64()+attempt)%9999 + 1
		var used bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM payments WHERE stablecoin->>'network'=$1 AND stablecoin->>'contract'=$2 AND stablecoin->>'address'=$3 AND stablecoin->>'amount'=$4)`, in.Network, asset.Contract, address, payments.StablecoinAmount(gross+candidate)).Scan(&used); err != nil {
			return p, err
		}
		if !used {
			fee = candidate
			break
		}
	}
	if fee == 0 {
		return p, payments.ErrAmountExhausted
	}
	p.Stablecoin = &payments.StablecoinPayment{Network: in.Network, Token: in.Token, Contract: asset.Contract, Address: address, Amount: payments.StablecoinAmount(gross + fee), FeeMicros: fee, StartBlock: head.Latest + 1, VAT: snapshot, ExplorerURL: n.Explorer + "/address/" + address}
	stablecoin, _ := json.Marshal(p.Stablecoin)
	bill, _ := json.Marshal(billing)
	vatData, _ := json.Marshal(snapshot)
	p, err = scanPayment(tx.QueryRowContext(ctx, `INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,amount_cents,currency,live,fee_mode,tax_mode,billing,stablecoin,invoice_mode,vat,expires_at) VALUES($1,$2,$3,$4,$5,$6,'usd',TRUE,'service','exclusive',$7,$8,'local',$9,NOW()+INTERVAL '720 hours') RETURNING `+paymentColumns, p.ID, org, pr.ID, actor, key, cents, bill, stablecoin, vatData))
	if err != nil {
		return p, paymentDBError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stablecoin_watchers(network,address,next_block) VALUES($1,$2,$3) ON CONFLICT(network,address) DO UPDATE SET next_block=CASE WHEN NOT EXISTS(SELECT 1 FROM payments p WHERE p.id<>$4 AND p.stablecoin->>'network'=$1 AND p.stablecoin->>'address'=$2 AND p.status IN ('pending','confirming') AND p.expires_at>clock_timestamp()) THEN GREATEST(stablecoin_watchers.next_block,EXCLUDED.next_block) ELSE stablecoin_watchers.next_block END`, in.Network, address, head.Latest+1, p.ID)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (r *SystemRepository) CancelStablecoin(ctx context.Context, actor, org, id string) (payments.Payment, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return payments.Payment{}, err
	}
	defer tx.Rollback()
	p, err := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, org))
	if err != nil {
		return p, err
	}
	if _, err = orgLock(ctx, tx, actor, org); err != nil {
		return payments.Payment{}, err
	}
	if p.Stablecoin == nil || p.Settled || (p.Status != "pending" && p.Status != "cancelled") {
		return p, payments.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE payments SET status='cancelled',updated_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return p, err
	}
	p.Status = "cancelled"
	if err = orgEvent(ctx, tx, org, actor, "payment_cancelled", map[string]any{"payment_id": id}); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func (r *SystemRepository) ScanStablecoins(ctx context.Context, chain payments.ChainReader) error {
	if err := r.ExpirePayments(ctx); err != nil {
		return err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT network,address FROM stablecoin_watchers w WHERE EXISTS(SELECT 1 FROM payments p WHERE p.stablecoin->>'network'=w.network AND p.stablecoin->>'address'=w.address AND p.status IN ('pending','confirming') AND p.expires_at>clock_timestamp()) ORDER BY checked_at,network,address LIMIT 6`)
	if err != nil {
		return err
	}
	type watcher struct{ network, address string }
	watchers := []watcher{}
	for rows.Next() {
		var w watcher
		if err = rows.Scan(&w.network, &w.address); err != nil {
			rows.Close()
			return err
		}
		watchers = append(watchers, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var first error
	for _, w := range watchers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := r.scanStablecoinWallet(ctx, chain, w.network, w.address); e != nil {
			if first == nil {
				first = e
			}
			_, _ = r.db.ExecContext(ctx, `UPDATE stablecoin_watchers SET checked_at=NOW() WHERE network=$1 AND address=$2`, w.network, w.address)
		}
	}
	return first
}
func (r *SystemRepository) scanStablecoinWallet(ctx context.Context, chain payments.ChainReader, network, address string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = setAuditActor(ctx, tx, identity.SystemOwnerID); err != nil {
		return err
	}
	var from uint64
	err = tx.QueryRowContext(ctx, `SELECT next_block FROM stablecoin_watchers WHERE network=$1 AND address=$2 FOR UPDATE SKIP LOCKED`, network, address).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM payments WHERE stablecoin->>'network'=$1 AND stablecoin->>'address'=$2 AND status IN ('pending','confirming') AND expires_at>clock_timestamp())`, network, address).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	head, err := chain.Head(ctx, network)
	if err != nil {
		return err
	}
	if head.Confirmed > head.Latest {
		return payments.ErrConflict
	}
	if from > head.Latest {
		_, err = tx.ExecContext(ctx, `UPDATE stablecoin_watchers SET checked_at=NOW() WHERE network=$1 AND address=$2`, network, address)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	end := min(from+999, head.Latest)
	transfers, err := chain.Transfers(ctx, network, address, from, end)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE stablecoin_transfers SET canonical=FALSE WHERE network=$1 AND address=$2 AND block_number BETWEEN $3 AND $4 AND NOT confirmed`, network, address, from, end)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE payments SET status='pending',stablecoin=stablecoin-'transaction_hash'-'log_index'-'block_number' WHERE stablecoin->>'network'=$1 AND stablecoin->>'address'=$2 AND status='confirming' AND (stablecoin->>'block_number')::bigint BETWEEN $3 AND $4`, network, address, from, end)
	if err != nil {
		return err
	}
	for _, t := range transfers {
		if t.Address != address || t.Block < from || t.Block > end || !payments.StablecoinAddress(t.Contract) {
			return payments.ErrConflict
		}
		confirmed := t.Block <= head.Confirmed
		observed, err := tx.ExecContext(ctx, `INSERT INTO stablecoin_transfers(network,transaction_hash,log_index,contract,address,amount,block_number,block_hash,confirmed) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(network,transaction_hash,log_index) DO UPDATE SET contract=EXCLUDED.contract,address=EXCLUDED.address,amount=EXCLUDED.amount,block_number=EXCLUDED.block_number,block_hash=EXCLUDED.block_hash,canonical=TRUE,confirmed=EXCLUDED.confirmed WHERE stablecoin_transfers.payment_id IS NULL OR (stablecoin_transfers.contract=EXCLUDED.contract AND stablecoin_transfers.address=EXCLUDED.address AND stablecoin_transfers.amount=EXCLUDED.amount AND stablecoin_transfers.block_hash=EXCLUDED.block_hash)`, network, t.Hash, t.Index, t.Contract, address, t.Amount, t.Block, t.BlockHash, confirmed)
		if err != nil {
			return err
		}
		count, err := observed.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return payments.ErrConflict
		}
		p, e := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE stablecoin->>'network'=$1 AND stablecoin->>'contract'=$2 AND stablecoin->>'address'=$3 AND stablecoin->>'amount'=$4 AND (stablecoin->>'start_block')::bigint<=$5 AND status IN ('pending','confirming') AND expires_at>clock_timestamp() FOR UPDATE`, network, t.Contract, address, t.Amount, t.Block))
		if errors.Is(e, payments.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		// A second transfer of the same amount remains unassigned; it is not another credit.
		var assigned bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stablecoin_transfers WHERE payment_id=$1 AND (network,transaction_hash,log_index)<>($2,$3,$4) AND canonical)`, p.ID, network, t.Hash, t.Index).Scan(&assigned); err != nil {
			return err
		}
		if assigned {
			continue
		}
		n, _, _ := payments.StablecoinAsset(network, p.Stablecoin.Token)
		p.Stablecoin.TransactionHash = t.Hash
		p.Stablecoin.LogIndex = t.Index
		p.Stablecoin.BlockNumber = t.Block
		p.Stablecoin.ExplorerURL = n.Explorer + "/tx/" + t.Hash
		data, _ := json.Marshal(p.Stablecoin)
		state := "confirming"
		if confirmed {
			state = "paid"
		}
		_, err = tx.ExecContext(ctx, `UPDATE payments SET stablecoin=$2,status=$3,updated_at=NOW() WHERE id=$1`, p.ID, data, state)
		if err != nil {
			return err
		}
		if confirmed {
			// Link a transfer only once, in the same transaction as ledger posting.
			result, e := tx.ExecContext(ctx, `UPDATE stablecoin_transfers SET payment_id=$4 WHERE network=$1 AND transaction_hash=$2 AND log_index=$3 AND payment_id IS NULL`, network, t.Hash, t.Index, p.ID)
			if e != nil {
				return e
			}
			count, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if count != 1 {
				return payments.ErrConflict
			}
			if _, err = applyPayment(ctx, tx, p, payments.Snapshot{Billing: p.Billing, Status: "paid", Paid: true}); err != nil {
				return err
			}
		}
	}
	next := from
	if head.Confirmed >= from {
		next = min(end, head.Confirmed) + 1
	}
	_, err = tx.ExecContext(ctx, `UPDATE stablecoin_watchers SET next_block=$3,checked_at=NOW() WHERE network=$1 AND address=$2`, network, address, next)
	if err != nil {
		return err
	}
	return tx.Commit()
}

var _ payments.StablecoinRepository = (*SystemRepository)(nil)
