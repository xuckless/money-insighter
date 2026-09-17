package store

import (
	"context"
	"encoding/json"
	"time"

	"plaidsync/internal/civil"
	"plaidsync/internal/money"
)

// Conventions for the row types in this file:
//
//   - Nullable columns are pointers (*string, *time.Time, *money.Amount,
//     *civil.Date); nil is SQL NULL. NOT NULL columns are plain values.
//   - Every field carries a `db` tag naming its column, so a SELECT that
//     lists exactly those columns can be collected with
//     pgx.RowToStructByName. Explicit Scan calls are equally fine.
//   - money.Amount and civil.Date are passed as query arguments directly
//     (they implement driver.Valuer, sent as text) and scanned back through
//     their sql.Scanner implementations; pgx materialises a NUMERIC as a
//     decimal string and a DATE as midnight UTC, which both types handle.
//   - JSONB columns are json.RawMessage: pgx sends it verbatim and scans it
//     back as a copy of the stored document (nil for NULL).
//   - Fields marked "read-only" are maintained by the database (defaults and
//     triggers) and are populated on read; the store ignores them on write.

// ItemStatus is the lifecycle state of a Plaid Item as stored in
// plaid_items.status. The database enforces the set of values.
type ItemStatus string

// Item statuses. They mirror Plaid's item error taxonomy: the three
// re-auth states are terminal until a human runs Link in update mode.
const (
	// ItemStatusActive means the item is healthy and syncable.
	ItemStatusActive ItemStatus = "active"
	// ItemStatusLoginRequired mirrors Plaid's ITEM_LOGIN_REQUIRED.
	ItemStatusLoginRequired ItemStatus = "login_required"
	// ItemStatusPendingExpiration mirrors Plaid's PENDING_EXPIRATION.
	ItemStatusPendingExpiration ItemStatus = "pending_expiration"
	// ItemStatusPermissionRevoked mirrors Plaid's USER_PERMISSION_REVOKED.
	ItemStatusPermissionRevoked ItemStatus = "permission_revoked"
	// ItemStatusError means the last sync failed with a non-reauth error;
	// last_error_* on the row says which.
	ItemStatusError ItemStatus = "error"
	// ItemStatusRemoved means /item/remove succeeded and the credential has
	// been purged. The row is kept for history; it is never synced again.
	ItemStatusRemoved ItemStatus = "removed"
)

// Item is a row of plaid_items without its credential. It never carries the
// access token in any form, so it is safe to hand to the API layer and to
// log (Raw is Plaid's /item/get payload, which contains no secrets).
type Item struct {
	ItemID          string  `db:"item_id"`
	InstitutionID   *string `db:"institution_id"`
	InstitutionName *string `db:"institution_name"`

	// Cursor is the /transactions/sync cursor saved after the last complete
	// pagination; nil means the item has never been synced.
	Cursor *string    `db:"cursor"`
	Status ItemStatus `db:"status"`

	LastErrorCode    *string    `db:"last_error_code"`
	LastErrorType    *string    `db:"last_error_type"`
	LastErrorMessage *string    `db:"last_error_message"`
	LastErrorAt      *time.Time `db:"last_error_at"`

	LastSuccessfulSyncAt *time.Time `db:"last_successful_sync_at"`
	ConsentExpiresAt     *time.Time `db:"consent_expires_at"`

	Raw       json.RawMessage `db:"raw"`
	CreatedAt time.Time       `db:"created_at"` // read-only
	UpdatedAt time.Time       `db:"updated_at"` // read-only
}

// Credential is the encrypted access token of an item: the AES-256-GCM
// blob (nonce || ciphertext || tag) and the version of the KEK that wrote
// it. Only internal/crypto can turn it back into a token.
type Credential struct {
	Ciphertext []byte `db:"encrypted_access_token"`
	KeyVersion uint32 `db:"key_version"`
}

// ItemError is what SetItemStatus records in last_error_* when a sync
// fails: Plaid's error_code and error_type, a human-readable message, and
// when it happened. Message must never contain an access token or a request
// body; callers pass Plaid's error_message, not the raw response.
type ItemError struct {
	Code    string
	Type    string
	Message string
	At      time.Time
}

// NewItem is the input to UpsertItem: everything known about an item right
// after /item/public_token/exchange and /item/get.
type NewItem struct {
	ItemID           string
	InstitutionID    *string
	InstitutionName  *string
	Credential       Credential
	ConsentExpiresAt *time.Time
	Raw              json.RawMessage
}

// Account is a row of plaid_accounts. The store fills ItemID from the
// locked item when applying a SyncBatch; callers building Accounts from
// Plaid responses may leave it empty.
type Account struct {
	AccountID    string  `db:"account_id"`
	ItemID       string  `db:"item_id"`
	Name         string  `db:"name"`
	OfficialName *string `db:"official_name"`
	Mask         *string `db:"mask"`
	Type         string  `db:"type"`
	Subtype      *string `db:"subtype"`

	CurrentBalance   *money.Amount `db:"current_balance"`
	AvailableBalance *money.Amount `db:"available_balance"`
	// CreditLimit is Plaid's balances.limit; the column is credit_limit
	// because LIMIT is a reserved word.
	CreditLimit *money.Amount `db:"credit_limit"`

	ISOCurrencyCode        *string    `db:"iso_currency_code"`
	UnofficialCurrencyCode *string    `db:"unofficial_currency_code"`
	BalanceLastUpdatedAt   *time.Time `db:"balance_last_updated_at"`

	Raw json.RawMessage `db:"raw"`

	FirstSeenAt  time.Time  `db:"first_seen_at"` // read-only
	LastSeenAt   time.Time  `db:"last_seen_at"`  // read-only
	MissingSince *time.Time `db:"missing_since"` // read-only; set when a sync stops listing the account
	UpdatedAt    time.Time  `db:"updated_at"`    // read-only
}

// Transaction is a row of transactions: one Plaid transaction in normalised
// columns plus the full payload in Raw. Rows are never deleted; RemovedAt
// marks a retraction and SupersededBy links a pending row to the posted row
// that replaced it. Consumers filter on those two fields.
type Transaction struct {
	TransactionID string `db:"transaction_id"`
	AccountID     string `db:"account_id"`
	ItemID        string `db:"item_id"`

	// Amount uses Plaid's sign convention: positive is money leaving the
	// account, negative is money arriving.
	Amount                 money.Amount `db:"amount"`
	ISOCurrencyCode        *string      `db:"iso_currency_code"`
	UnofficialCurrencyCode *string      `db:"unofficial_currency_code"`

	Date               civil.Date  `db:"date"`
	AuthorizedDate     *civil.Date `db:"authorized_date"`
	DateTime           *time.Time  `db:"datetime"`
	AuthorizedDateTime *time.Time  `db:"authorized_datetime"`

	Name             string  `db:"name"`
	MerchantName     *string `db:"merchant_name"`
	MerchantEntityID *string `db:"merchant_entity_id"`

	Pending bool `db:"pending"`
	// PendingTransactionID is set by Plaid on a posted row and names the
	// pending row it replaces.
	PendingTransactionID *string `db:"pending_transaction_id"`

	PFCPrimary      *string `db:"pfc_primary"`
	PFCDetailed     *string `db:"pfc_detailed"`
	PFCConfidence   *string `db:"pfc_confidence"`
	PaymentChannel  *string `db:"payment_channel"`
	TransactionCode *string `db:"transaction_code"`

	Raw json.RawMessage `db:"raw"`

	// SupersededBy is set by the store on a pending row once its posted
	// successor has been seen; SupersededAt records when.
	SupersededBy *string    `db:"superseded_by"` // read-only
	SupersededAt *time.Time `db:"superseded_at"` // read-only
	RemovedAt    *time.Time `db:"removed_at"`    // read-only; soft delete
	FirstSeenAt  time.Time  `db:"first_seen_at"` // read-only
	UpdatedAt    time.Time  `db:"updated_at"`    // read-only
}

// JobKind says what triggered a sync job. It doubles as the trigger column
// of sync_runs. The database enforces the set of values.
type JobKind string

// Job kinds.
const (
	// JobKindInitial is the first sync after an item is linked.
	JobKindInitial JobKind = "initial"
	// JobKindManual is a sync requested through the API.
	JobKindManual JobKind = "manual"
	// JobKindWebhook is a sync started by SYNC_UPDATES_AVAILABLE.
	JobKindWebhook JobKind = "webhook"
	// JobKindScheduled is a sync started by the in-process scheduler.
	JobKindScheduled JobKind = "scheduled"
)

// JobState is the lifecycle state of a sync job. The database enforces the
// set of values.
type JobState string

// Job states. queued and running are transient; the other three are
// terminal and are the only values FinishJob accepts.
const (
	// JobStateQueued is the state a job is created in.
	JobStateQueued JobState = "queued"
	// JobStateRunning is set by StartJob.
	JobStateRunning JobState = "running"
	// JobStateSucceeded is a terminal state: the sync completed.
	JobStateSucceeded JobState = "succeeded"
	// JobStateFailed is a terminal state: the sync gave up with an error.
	JobStateFailed JobState = "failed"
	// JobStateSkipped is a terminal state: the sync did not run (debounced,
	// item locked, item not syncable).
	JobStateSkipped JobState = "skipped"
)

// IsTerminal reports whether the state is one a job can finish in.
func (s JobState) IsTerminal() bool {
	switch s {
	case JobStateSucceeded, JobStateFailed, JobStateSkipped:
		return true
	}
	return false
}

// Job is a row of sync_jobs: the unit the API hands back as a 202 and that
// clients poll on GET /v1/jobs/{job_id}.
type Job struct {
	// JobID is a v4 UUID in its canonical text form.
	JobID  string   `db:"job_id"`
	ItemID string   `db:"item_id"`
	Kind   JobKind  `db:"kind"`
	State  JobState `db:"state"`

	CreatedAt  time.Time  `db:"created_at"` // read-only
	StartedAt  *time.Time `db:"started_at"`
	FinishedAt *time.Time `db:"finished_at"`

	ErrorCode    *string `db:"error_code"`
	ErrorMessage *string `db:"error_message"`
}

// SyncOutcome classifies how a sync run ended. It is the outcome column of
// sync_runs; the database enforces the set of values.
type SyncOutcome string

// Sync outcomes. They follow the spec's error buckets: retryable_error is
// backed off and retried, needs_reauth waits for a human, fatal is a
// configuration problem, and the rest describe runs that did not sync.
const (
	// SyncOutcomeSuccess means the pagination completed and was committed.
	SyncOutcomeSuccess SyncOutcome = "success"
	// SyncOutcomeRetryableError is a transient Plaid or institution failure.
	SyncOutcomeRetryableError SyncOutcome = "retryable_error"
	// SyncOutcomeNeedsReauth means the item needs Link in update mode.
	SyncOutcomeNeedsReauth SyncOutcome = "needs_reauth"
	// SyncOutcomeFatal is a configuration failure such as INVALID_API_KEYS.
	SyncOutcomeFatal SyncOutcome = "fatal"
	// SyncOutcomeLocked means another sync held the item lock.
	SyncOutcomeLocked SyncOutcome = "locked"
	// SyncOutcomeCanceled means the run's context was canceled (shutdown).
	SyncOutcomeCanceled SyncOutcome = "canceled"
	// SyncOutcomeError is any other failure, including database errors.
	SyncOutcomeError SyncOutcome = "error"
)

// SyncRun is a row of sync_runs, the audit record of one sync attempt.
// RunID is assigned by the database; RecordSyncRun ignores it on input and
// returns the assigned value.
type SyncRun struct {
	RunID  int64   `db:"run_id"` // read-only
	ItemID string  `db:"item_id"`
	JobID  *string `db:"job_id"` // UUID text; nil for runs not tied to a job
	// Trigger is what started the run; it uses the same values as JobKind.
	Trigger    JobKind   `db:"trigger"`
	StartedAt  time.Time `db:"started_at"`
	FinishedAt time.Time `db:"finished_at"`

	CursorBefore *string `db:"cursor_before"`
	CursorAfter  *string `db:"cursor_after"`

	// Pages is the number of /transactions/sync calls made.
	Pages int `db:"pages"`
	// Added, Modified and Removed are the counts Plaid reported.
	Added    int `db:"added"`
	Modified int `db:"modified"`
	Removed  int `db:"removed"`
	// Inserted, Updated and Superseded are what the store actually did.
	Inserted   int `db:"inserted"`
	Updated    int `db:"updated"`
	Superseded int `db:"superseded"`

	AccountsSeen    int `db:"accounts_seen"`
	AccountsMissing int `db:"accounts_missing"`

	Outcome      SyncOutcome `db:"outcome"`
	ErrorCode    *string     `db:"error_code"`
	ErrorType    *string     `db:"error_type"`
	ErrorMessage *string     `db:"error_message"`
	// RequestID is Plaid's request_id of the failing call, for support.
	RequestID *string `db:"request_id"`
}

// SyncBatch is everything one complete pagination of /transactions/sync
// produced, ready to be applied atomically with the new cursor.
type SyncBatch struct {
	// Accounts are the accounts Plaid listed on the final page. ItemID is
	// filled in by the store. An empty list means "no account information",
	// not "no accounts": nothing is marked missing.
	Accounts []Account
	// Upserts are Plaid's added and modified transactions, in the order
	// received. The store dedupes by TransactionID keeping the LAST
	// occurrence, because a multi-row INSERT ... ON CONFLICT cannot touch
	// the same row twice.
	Upserts []Transaction
	// Removed are the transaction_ids Plaid reported as removed. Unknown
	// ids are ignored.
	Removed []string
	// NextCursor is the cursor Plaid returned on the final page; it is saved
	// in the same transaction as the rows.
	NextCursor string
	// Added and Modified are Plaid's counts, recorded in the audit row.
	Added, Modified int
}

// ApplyResult reports what ApplySyncBatch actually changed.
type ApplyResult struct {
	// Inserted counts transactions that did not exist before.
	Inserted int
	// Updated counts existing transactions whose raw payload differed.
	Updated int
	// Unchanged counts upserts whose raw payload was identical (a replay).
	Unchanged int
	// Removed counts transactions soft-deleted for the first time.
	Removed int
	// Superseded counts pending rows newly linked to a posted successor.
	Superseded int
	// AccountsUpserted counts accounts written.
	AccountsUpserted int
	// MissingAccountIDs lists every account of the item that is currently
	// flagged missing, whether it was flagged on this run or earlier, so
	// the engine can log it loudly every time. It is only computed when the
	// batch listed at least one account.
	MissingAccountIDs []string
}

// ItemTx is the view of an item-locked transaction handed to the sync
// engine by WithItemLock. Everything done through it is committed or rolled
// back together with the cursor update, and the advisory lock is held for
// its whole lifetime, including the caller's network calls to Plaid.
type ItemTx interface {
	// Item returns the item as loaded inside the transaction, after the lock
	// was taken (SELECT ... FOR NO KEY UPDATE).
	Item() *Item
	// Credential reads the encrypted access token inside the transaction.
	// Call it as late as possible and drop the result as soon as possible.
	// It returns ErrNotFound for a removed item.
	Credential(ctx context.Context) (*Credential, error)
	// ApplySyncBatch writes accounts, transactions, soft deletes, pending
	// links and the new cursor in this transaction. It fails without
	// writing anything if the item has been removed.
	ApplySyncBatch(ctx context.Context, b SyncBatch) (ApplyResult, error)
	// RecordSyncRun inserts the audit row in this transaction and returns
	// its run_id.
	RecordSyncRun(ctx context.Context, r SyncRun) (int64, error)
}
