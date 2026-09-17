package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"plaidsync/internal/civil"
	"plaidsync/internal/money"
)

// This file holds the recurring transactions add-on: the streams Plaid
// reports for an item and the record of the item's last refresh. None of
// it touches the item's status, cursor or last_error_* columns; the add-on
// failing says nothing about whether the item syncs.

// Stream directions, as stored in plaid_recurring_streams.direction.
const (
	StreamDirectionInflow  = "inflow"
	StreamDirectionOutflow = "outflow"
)

// RecurringStream is a row of plaid_recurring_streams: one stream from
// /transactions/recurring/get in normalised columns plus the full object in
// Raw. Amounts use Plaid's sign convention (positive is money out).
type RecurringStream struct {
	StreamID  string `db:"stream_id"`
	ItemID    string `db:"item_id"`
	AccountID string `db:"account_id"`
	Direction string `db:"direction"`

	Description  string  `db:"description"`
	MerchantName *string `db:"merchant_name"`
	PFCPrimary   *string `db:"pfc_primary"`
	PFCDetailed  *string `db:"pfc_detailed"`

	Frequency         string      `db:"frequency"`
	FirstDate         civil.Date  `db:"first_date"`
	LastDate          civil.Date  `db:"last_date"`
	PredictedNextDate *civil.Date `db:"predicted_next_date"`

	AverageAmount          *money.Amount `db:"average_amount"`
	LastAmount             *money.Amount `db:"last_amount"`
	ISOCurrencyCode        *string       `db:"iso_currency_code"`
	UnofficialCurrencyCode *string       `db:"unofficial_currency_code"`

	IsActive       bool     `db:"is_active"`
	Status         string   `db:"status"`
	TransactionIDs []string `db:"transaction_ids"`

	Raw json.RawMessage `db:"raw"`

	RemovedAt   *time.Time `db:"removed_at"`    // read-only
	FirstSeenAt time.Time  `db:"first_seen_at"` // read-only
	UpdatedAt   time.Time  `db:"updated_at"`    // read-only
}

// recurringStreamSelectColumns is the column list that maps onto
// RecurringStream.
const recurringStreamSelectColumns = `
	stream_id, item_id, account_id, direction,
	description, merchant_name, pfc_primary, pfc_detailed,
	frequency, first_date, last_date, predicted_next_date,
	average_amount, last_amount, iso_currency_code, unofficial_currency_code,
	is_active, status, transaction_ids, raw,
	removed_at, first_seen_at, updated_at`

// upsertStreamSQL writes one stream. It inserts nothing when the stream's
// account is unknown: /transactions/recurring/get can name an account the
// last sync has not written yet, and one such stream must not abort the
// whole refresh with a foreign key violation. The next refresh picks it up.
const upsertStreamSQL = `
	INSERT INTO plaid_recurring_streams (
		stream_id, item_id, account_id, direction,
		description, merchant_name, pfc_primary, pfc_detailed,
		frequency, first_date, last_date, predicted_next_date,
		average_amount, last_amount, iso_currency_code, unofficial_currency_code,
		is_active, status, transaction_ids, raw, removed_at
	)
	SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, NULL
	WHERE EXISTS (SELECT 1 FROM plaid_accounts WHERE account_id = $3)
	ON CONFLICT (stream_id) DO UPDATE SET
		item_id                  = EXCLUDED.item_id,
		account_id               = EXCLUDED.account_id,
		direction                = EXCLUDED.direction,
		description              = EXCLUDED.description,
		merchant_name            = EXCLUDED.merchant_name,
		pfc_primary              = EXCLUDED.pfc_primary,
		pfc_detailed             = EXCLUDED.pfc_detailed,
		frequency                = EXCLUDED.frequency,
		first_date               = EXCLUDED.first_date,
		last_date                = EXCLUDED.last_date,
		predicted_next_date      = EXCLUDED.predicted_next_date,
		average_amount           = EXCLUDED.average_amount,
		last_amount              = EXCLUDED.last_amount,
		iso_currency_code        = EXCLUDED.iso_currency_code,
		unofficial_currency_code = EXCLUDED.unofficial_currency_code,
		is_active                = EXCLUDED.is_active,
		status                   = EXCLUDED.status,
		transaction_ids          = EXCLUDED.transaction_ids,
		raw                      = EXCLUDED.raw,
		removed_at               = NULL`

// validateStream checks the fields the schema needs before the row reaches
// the database.
func validateStream(s RecurringStream) error {
	if s.StreamID == "" {
		return errors.New("stream id is empty")
	}
	if s.AccountID == "" {
		return fmt.Errorf("stream %s: account id is empty", s.StreamID)
	}
	if s.Direction != StreamDirectionInflow && s.Direction != StreamDirectionOutflow {
		return fmt.Errorf("stream %s: unknown direction %q", s.StreamID, s.Direction)
	}
	if len(s.Raw) == 0 {
		return fmt.Errorf("stream %s: raw payload is empty", s.StreamID)
	}
	if err := validateAmountScale("average amount", s.AverageAmount); err != nil {
		return fmt.Errorf("stream %s: %w", s.StreamID, err)
	}
	if err := validateAmountScale("last amount", s.LastAmount); err != nil {
		return fmt.Errorf("stream %s: %w", s.StreamID, err)
	}
	return nil
}

// ReplaceRecurringStreams makes streams the item's complete stream list in
// one transaction: every stream is upserted (and un-removed), every other
// live stream of the item gets removed_at, and the item's refresh is
// recorded as a success (recurring_checked_at and recurring_refreshed_at
// set, recurring_error_* cleared). It returns ErrNotFound when the item
// does not exist.
func (s *Store) ReplaceRecurringStreams(ctx context.Context, itemID string, streams []RecurringStream) error {
	if itemID == "" {
		return errors.New("store: replace recurring streams: item id is empty")
	}
	ids := make([]string, 0, len(streams))
	batch := &pgx.Batch{}
	for _, st := range streams {
		if err := validateStream(st); err != nil {
			return fmt.Errorf("store: replace recurring streams: %w", err)
		}
		ids = append(ids, st.StreamID)
		txIDs := st.TransactionIDs
		if txIDs == nil {
			txIDs = []string{}
		}
		batch.Queue(upsertStreamSQL,
			st.StreamID, itemID, st.AccountID, st.Direction,
			st.Description, st.MerchantName, st.PFCPrimary, st.PFCDetailed,
			st.Frequency, st.FirstDate, st.LastDate, st.PredictedNextDate,
			st.AverageAmount, st.LastAmount, st.ISOCurrencyCode, st.UnofficialCurrencyCode,
			st.IsActive, st.Status, txIDs, st.Raw,
		)
	}
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Lock the item row first so a concurrent refresh of the same item
		// waits instead of interleaving its upserts with this one's.
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM plaid_items WHERE item_id = $1 FOR NO KEY UPDATE`, itemID,
		).Scan(&exists); err != nil {
			return notFoundIfNoRows(err)
		}
		if batch.Len() > 0 {
			if err := tx.SendBatch(ctx, batch).Close(); err != nil {
				return fmt.Errorf("upsert streams: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE plaid_recurring_streams
			SET removed_at = clock_timestamp()
			WHERE item_id = $1 AND removed_at IS NULL AND stream_id <> ALL($2::text[])`,
			itemID, ids); err != nil {
			return fmt.Errorf("remove stale streams: %w", err)
		}
		_, err := tx.Exec(ctx, `
			UPDATE plaid_items
			SET recurring_checked_at = clock_timestamp(),
			    recurring_refreshed_at = clock_timestamp(),
			    recurring_error_code = NULL,
			    recurring_error_message = NULL
			WHERE item_id = $1`, itemID)
		return err
	})
	if err != nil {
		return fmt.Errorf("store: replace recurring streams: %w", err)
	}
	return nil
}

// RecordRecurringFailure records a failed refresh on the item: it sets
// recurring_checked_at and recurring_error_*, and leaves the streams from
// the last successful refresh in place. It returns ErrNotFound when the
// item does not exist.
func (s *Store) RecordRecurringFailure(ctx context.Context, itemID, code, message string) error {
	var codeP *string
	if code != "" {
		codeP = &code
	}
	err := rowsAffectedOrNotFound(s.pool.Exec(ctx, `
		UPDATE plaid_items
		SET recurring_checked_at = clock_timestamp(),
		    recurring_error_code = $2,
		    recurring_error_message = $3
		WHERE item_id = $1`,
		itemID, codeP, message))
	if err != nil {
		return fmt.Errorf("store: record recurring failure: %w", err)
	}
	return nil
}

// ItemsDueRecurringRefresh returns the ids of syncable items (active or
// error) that have synced at least once and whose recurring streams were
// never checked or were last checked before cutoff, oldest check first.
func (s *Store) ItemsDueRecurringRefresh(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT item_id FROM plaid_items
		WHERE status IN ('active', 'error')
		  AND cursor IS NOT NULL
		  AND (recurring_checked_at IS NULL OR recurring_checked_at < $1)
		ORDER BY recurring_checked_at NULLS FIRST, item_id`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("store: items due recurring refresh: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("store: items due recurring refresh: %w", err)
	}
	return ids, nil
}

// ListRecurringStreams returns the item's streams, removed ones included,
// ordered by stream_id. An unknown item yields an empty list.
func (s *Store) ListRecurringStreams(ctx context.Context, itemID string) ([]RecurringStream, error) {
	streams, err := scanAllByName[RecurringStream](ctx, s.pool,
		`SELECT `+recurringStreamSelectColumns+` FROM plaid_recurring_streams WHERE item_id = $1 ORDER BY stream_id`,
		itemID)
	if err != nil {
		return nil, fmt.Errorf("store: list recurring streams: %w", err)
	}
	return streams, nil
}

// RecurringCheck is the outcome of an item's last recurring refresh, as
// recorded in plaid_items.recurring_*.
type RecurringCheck struct {
	CheckedAt    *time.Time `db:"recurring_checked_at"`
	RefreshedAt  *time.Time `db:"recurring_refreshed_at"`
	ErrorCode    *string    `db:"recurring_error_code"`
	ErrorMessage *string    `db:"recurring_error_message"`
}

// GetRecurringCheck returns the item's recurring refresh record. It
// returns ErrNotFound when the item does not exist.
func (s *Store) GetRecurringCheck(ctx context.Context, itemID string) (*RecurringCheck, error) {
	c, err := scanOneByName[RecurringCheck](ctx, s.pool, `
		SELECT recurring_checked_at, recurring_refreshed_at, recurring_error_code, recurring_error_message
		FROM plaid_items WHERE item_id = $1`, itemID)
	if err != nil {
		return nil, fmt.Errorf("store: get recurring check: %w", err)
	}
	return c, nil
}
