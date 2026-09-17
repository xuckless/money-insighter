package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// This file holds the transaction half of ApplySyncBatch: the upsert of
// added and modified rows, the soft delete of removed ones, and the two
// directions of the pending -> posted link. Everything here runs inside an
// item-locked transaction, so the helpers take pgx.Tx.

// upsertTransactionSQL writes one transaction. On conflict it replaces
// every column except:
//
//   - transaction_id, the key;
//   - item_id, which never moves;
//   - first_seen_at, the audit of when we first saw the row;
//   - superseded_by / superseded_at, which only the pending link writes;
//   - removed_at, the soft delete. Plaid does not resurrect a removed
//     transaction, so a re-delivered row for something we soft-deleted
//     keeps its removed_at; the row's data is still refreshed.
//
// The WHERE makes an identical replay a no-op: nothing is written, the
// trigger does not fire, and RETURNING yields no row, which the caller
// counts as unchanged. When a row is written, xmax = 0 is true only for a
// freshly inserted tuple, which separates inserts from updates.
// first_seen_at is set explicitly with clock_timestamp() rather than left
// to the column default, which is now(): see ApplySyncBatch.
const upsertTransactionSQL = `
	INSERT INTO transactions (
		transaction_id, account_id, item_id, amount,
		iso_currency_code, unofficial_currency_code,
		date, authorized_date, datetime, authorized_datetime,
		name, merchant_name, merchant_entity_id,
		pending, pending_transaction_id,
		pfc_primary, pfc_detailed, pfc_confidence, payment_channel, transaction_code,
		raw, first_seen_at
	)
	VALUES (
		$1, $2, $3, $4,
		$5, $6,
		$7, $8, $9, $10,
		$11, $12, $13,
		$14, $15,
		$16, $17, $18, $19, $20,
		$21, clock_timestamp()
	)
	ON CONFLICT (transaction_id) DO UPDATE SET
		account_id               = EXCLUDED.account_id,
		amount                   = EXCLUDED.amount,
		iso_currency_code        = EXCLUDED.iso_currency_code,
		unofficial_currency_code = EXCLUDED.unofficial_currency_code,
		date                     = EXCLUDED.date,
		authorized_date          = EXCLUDED.authorized_date,
		datetime                 = EXCLUDED.datetime,
		authorized_datetime      = EXCLUDED.authorized_datetime,
		name                     = EXCLUDED.name,
		merchant_name            = EXCLUDED.merchant_name,
		merchant_entity_id       = EXCLUDED.merchant_entity_id,
		pending                  = EXCLUDED.pending,
		pending_transaction_id   = EXCLUDED.pending_transaction_id,
		pfc_primary              = EXCLUDED.pfc_primary,
		pfc_detailed             = EXCLUDED.pfc_detailed,
		pfc_confidence           = EXCLUDED.pfc_confidence,
		payment_channel          = EXCLUDED.payment_channel,
		transaction_code         = EXCLUDED.transaction_code,
		raw                      = EXCLUDED.raw
	WHERE transactions.raw IS DISTINCT FROM EXCLUDED.raw
	RETURNING (xmax = 0) AS inserted`

// softDeleteTransactionsSQL marks the given transactions of the item as
// removed. The before CTE captures removed_at ahead of the update so the
// statement can report how many rows were removed for the first time;
// COALESCE keeps the original timestamp on rows removed earlier. Ids that
// do not exist (or belong to another item) simply match nothing.
const softDeleteTransactionsSQL = `
	WITH before AS (
		SELECT transaction_id, removed_at
		FROM transactions
		WHERE transaction_id = ANY($1::text[]) AND item_id = $2
	),
	updated AS (
		UPDATE transactions t
		SET removed_at = COALESCE(t.removed_at, clock_timestamp())
		FROM before b
		WHERE t.transaction_id = b.transaction_id
		RETURNING b.removed_at AS removed_at_before
	)
	SELECT count(*) FROM updated WHERE removed_at_before IS NULL`

// linkPendingForwardSQL links pending rows to the posted rows that name
// them: for each (pending, posted) pair taken from a posted row's
// pending_transaction_id, the pending row (if it exists) gets
// superseded_by = posted. Rows already linked to that posted row are left
// alone so the count is "newly linked"; a row linked to a different posted
// id is re-pointed (Plaid's latest word wins) and its superseded_at kept.
const linkPendingForwardSQL = `
	UPDATE transactions p
	SET superseded_by = l.posted,
	    superseded_at = COALESCE(p.superseded_at, clock_timestamp())
	FROM unnest($1::text[], $2::text[]) AS l(pending, posted)
	WHERE p.transaction_id = l.pending
	  AND p.item_id = $3
	  AND p.superseded_by IS DISTINCT FROM l.posted`

// linkPendingReverseSQL covers the other arrival order: a pending row that
// is written after its posted successor (later in the same batch, or in a
// later batch). For every upserted row, if some transaction of the item
// names it as its pending_transaction_id, link it to that transaction.
const linkPendingReverseSQL = `
	UPDATE transactions p
	SET superseded_by = t.transaction_id,
	    superseded_at = COALESCE(p.superseded_at, clock_timestamp())
	FROM transactions t
	WHERE t.item_id = $1
	  AND t.pending_transaction_id = p.transaction_id
	  AND p.item_id = $1
	  AND p.transaction_id = ANY($2::text[])
	  AND p.superseded_by IS DISTINCT FROM t.transaction_id`

// validateTransaction checks the fields the schema needs before the row
// reaches the database, so a bad input produces a clear error rather than
// a constraint violation. Date is validated by its own type when encoded
// (an invalid Date refuses to produce a value); Amount is checked here for
// scale, because NUMERIC(14,2) would otherwise round a sub-cent value
// silently and the column would no longer agree with raw.
func validateTransaction(tr Transaction) error {
	if tr.TransactionID == "" {
		return errors.New("transaction id is empty")
	}
	if tr.AccountID == "" {
		return fmt.Errorf("transaction %s: account id is empty", tr.TransactionID)
	}
	if tr.Date.IsZero() {
		return fmt.Errorf("transaction %s: date is empty", tr.TransactionID)
	}
	if len(tr.Raw) == 0 {
		return fmt.Errorf("transaction %s: raw payload is empty", tr.TransactionID)
	}
	if err := validateAmountScale("amount", &tr.Amount); err != nil {
		return fmt.Errorf("transaction %s: %w", tr.TransactionID, err)
	}
	return nil
}

// dedupeTransactions returns transactions with each TransactionID
// appearing once. When an id repeats (Plaid reporting a row as added and
// then modified within one pagination), the LAST occurrence wins, because
// it is the latest state; it keeps the position of the first occurrence so
// the output order is stable.
func dedupeTransactions(transactions []Transaction) []Transaction {
	out := make([]Transaction, 0, len(transactions))
	index := make(map[string]int, len(transactions))
	for _, tr := range transactions {
		if i, seen := index[tr.TransactionID]; seen {
			out[i] = tr
			continue
		}
		index[tr.TransactionID] = len(out)
		out = append(out, tr)
	}
	return out
}

// upsertCounts is what upsertTransactions learned from RETURNING.
type upsertCounts struct {
	inserted, updated, unchanged int
}

// upsertTransactions writes transactions (already deduped) for the item in
// one pipelined batch: one statement per row, all sent in a single round
// trip, results read back in order. Every row is stamped with the locked
// item's id, whatever the caller left in Transaction.ItemID.
//
// money.Amount and civil.Date arguments are sent through their
// driver.Valuer text forms and land in NUMERIC and DATE exactly; nil
// pointers become NULL; json.RawMessage is sent verbatim to JSONB.
func upsertTransactions(ctx context.Context, tx pgx.Tx, itemID string, transactions []Transaction) (upsertCounts, error) {
	var counts upsertCounts
	if len(transactions) == 0 {
		return counts, nil
	}

	batch := &pgx.Batch{}
	for _, tr := range transactions {
		if err := validateTransaction(tr); err != nil {
			return counts, err
		}
		batch.Queue(upsertTransactionSQL,
			tr.TransactionID, tr.AccountID, itemID, tr.Amount,
			tr.ISOCurrencyCode, tr.UnofficialCurrencyCode,
			tr.Date, tr.AuthorizedDate, tr.DateTime, tr.AuthorizedDateTime,
			tr.Name, tr.MerchantName, tr.MerchantEntityID,
			tr.Pending, tr.PendingTransactionID,
			tr.PFCPrimary, tr.PFCDetailed, tr.PFCConfidence, tr.PaymentChannel, tr.TransactionCode,
			tr.Raw,
		).QueryRow(func(row pgx.Row) error {
			var inserted bool
			switch err := row.Scan(&inserted); {
			case errors.Is(err, pgx.ErrNoRows):
				// The DO UPDATE ... WHERE excluded the row: identical replay.
				counts.unchanged++
			case err != nil:
				return err
			case inserted:
				counts.inserted++
			default:
				counts.updated++
			}
			return nil
		})
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		if isForeignKeyViolation(err) {
			return upsertCounts{}, fmt.Errorf("upsert transactions: a transaction references an account that is neither in the batch nor in the database: %w", err)
		}
		return upsertCounts{}, fmt.Errorf("upsert transactions: %w", err)
	}
	return counts, nil
}

// softDeleteTransactions sets removed_at on the given transactions of the
// item and returns how many of them were not already removed. Unknown ids
// are ignored; removing a row twice keeps its original removed_at.
func softDeleteTransactions(ctx context.Context, tx pgx.Tx, itemID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var removed int
	if err := tx.QueryRow(ctx, softDeleteTransactionsSQL, ids, itemID).Scan(&removed); err != nil {
		return 0, fmt.Errorf("soft delete transactions: %w", err)
	}
	return removed, nil
}

// linkPendingTransactions reconciles pending rows with their posted
// successors among the rows just upserted, in both arrival orders, and
// returns how many pending rows were newly linked. The forward pass follows
// pending_transaction_id on upserted posted rows; the reverse pass finds,
// for every upserted row, a stored row that names it. A pair present in
// both passes is counted once because the second pass sees it already
// linked.
func linkPendingTransactions(ctx context.Context, tx pgx.Tx, itemID string, upserted []Transaction) (int, error) {
	if len(upserted) == 0 {
		return 0, nil
	}

	ids := make([]string, 0, len(upserted))
	var pending, posted []string
	for _, tr := range upserted {
		ids = append(ids, tr.TransactionID)
		if tr.PendingTransactionID != nil && *tr.PendingTransactionID != "" {
			pending = append(pending, *tr.PendingTransactionID)
			posted = append(posted, tr.TransactionID)
		}
	}

	total := 0
	if len(pending) > 0 {
		tag, err := tx.Exec(ctx, linkPendingForwardSQL, pending, posted, itemID)
		if err != nil {
			return 0, fmt.Errorf("link pending transactions (forward): %w", err)
		}
		total += int(tag.RowsAffected())
	}

	tag, err := tx.Exec(ctx, linkPendingReverseSQL, itemID, ids)
	if err != nil {
		return 0, fmt.Errorf("link pending transactions (reverse): %w", err)
	}
	total += int(tag.RowsAffected())
	return total, nil
}
