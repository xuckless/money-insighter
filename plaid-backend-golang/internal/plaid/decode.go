package plaid

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"plaidsync/internal/civil"
	"plaidsync/internal/money"
	"plaidsync/internal/store"
)

// The decoders in this file turn Plaid response bodies into the store's
// row types. They read the raw JSON with json.Number so amounts arrive as
// the exact decimal text Plaid sent, and they keep each account and
// transaction object verbatim in Raw. Unknown fields are ignored: Plaid
// adds fields over time and Raw preserves them anyway.

// decodeNumber parses a JSON number into an Amount. nil (a JSON null or an
// absent field) yields nil.
func decodeNumber(n *json.Number, field string) (*money.Amount, error) {
	if n == nil {
		return nil, nil
	}
	a, err := money.Parse(n.String())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return &a, nil
}

// decodeDate parses a Plaid YYYY-MM-DD date. nil yields nil.
func decodeDate(s *string, field string) (*civil.Date, error) {
	if s == nil {
		return nil, nil
	}
	d, err := civil.ParseDate(*s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return &d, nil
}

// decodeTime parses a Plaid RFC 3339 timestamp; Plaid sometimes sends
// "2023-01-01T00:00:00Z" and sometimes a value with a fractional second or
// an offset, all of which time.RFC3339Nano accepts. nil yields nil.
func decodeTime(s *string, field string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	t = t.UTC()
	return &t, nil
}

// newDecoder returns a decoder over b that keeps numbers as json.Number.
func newDecoder(b []byte) *json.Decoder {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d
}

// rawAccount is the subset of a Plaid account object plaid_accounts keeps.
type rawAccount struct {
	AccountID    string  `json:"account_id"`
	Name         string  `json:"name"`
	OfficialName *string `json:"official_name"`
	Mask         *string `json:"mask"`
	Type         string  `json:"type"`
	Subtype      *string `json:"subtype"`
	Balances     struct {
		Available              *json.Number `json:"available"`
		Current                *json.Number `json:"current"`
		Limit                  *json.Number `json:"limit"`
		ISOCurrencyCode        *string      `json:"iso_currency_code"`
		UnofficialCurrencyCode *string      `json:"unofficial_currency_code"`
		LastUpdatedDatetime    *string      `json:"last_updated_datetime"`
	} `json:"balances"`
}

// DecodeAccount translates one Plaid account object into a store.Account
// with Raw set to the object itself and ItemID left empty.
func DecodeAccount(raw json.RawMessage) (store.Account, error) {
	var r rawAccount
	if err := newDecoder(raw).Decode(&r); err != nil {
		return store.Account{}, fmt.Errorf("plaid: decode account: %w", err)
	}
	if r.AccountID == "" {
		return store.Account{}, fmt.Errorf("plaid: decode account: account_id is empty")
	}
	if r.Name == "" || r.Type == "" {
		return store.Account{}, fmt.Errorf("plaid: decode account %s: name or type is empty", r.AccountID)
	}
	var (
		a   store.Account
		err error
	)
	a.AccountID = r.AccountID
	a.Name = r.Name
	a.OfficialName = r.OfficialName
	a.Mask = r.Mask
	a.Type = r.Type
	a.Subtype = r.Subtype
	if a.CurrentBalance, err = decodeNumber(r.Balances.Current, "balances.current"); err != nil {
		return store.Account{}, fmt.Errorf("plaid: decode account %s: %w", r.AccountID, err)
	}
	if a.AvailableBalance, err = decodeNumber(r.Balances.Available, "balances.available"); err != nil {
		return store.Account{}, fmt.Errorf("plaid: decode account %s: %w", r.AccountID, err)
	}
	if a.CreditLimit, err = decodeNumber(r.Balances.Limit, "balances.limit"); err != nil {
		return store.Account{}, fmt.Errorf("plaid: decode account %s: %w", r.AccountID, err)
	}
	a.ISOCurrencyCode = r.Balances.ISOCurrencyCode
	a.UnofficialCurrencyCode = r.Balances.UnofficialCurrencyCode
	if a.BalanceLastUpdatedAt, err = decodeTime(r.Balances.LastUpdatedDatetime, "balances.last_updated_datetime"); err != nil {
		return store.Account{}, fmt.Errorf("plaid: decode account %s: %w", r.AccountID, err)
	}
	a.Raw = compact(raw)
	return a, nil
}

// rawTransaction is the subset of a Plaid transaction object the
// transactions table keeps in columns.
type rawTransaction struct {
	TransactionID           string       `json:"transaction_id"`
	AccountID               string       `json:"account_id"`
	Amount                  *json.Number `json:"amount"`
	ISOCurrencyCode         *string      `json:"iso_currency_code"`
	UnofficialCurrencyCode  *string      `json:"unofficial_currency_code"`
	Date                    *string      `json:"date"`
	AuthorizedDate          *string      `json:"authorized_date"`
	Datetime                *string      `json:"datetime"`
	AuthorizedDatetime      *string      `json:"authorized_datetime"`
	Name                    string       `json:"name"`
	MerchantName            *string      `json:"merchant_name"`
	MerchantEntityID        *string      `json:"merchant_entity_id"`
	Pending                 bool         `json:"pending"`
	PendingTransactionID    *string      `json:"pending_transaction_id"`
	PaymentChannel          *string      `json:"payment_channel"`
	TransactionCode         *string      `json:"transaction_code"`
	PersonalFinanceCategory *struct {
		Primary         *string `json:"primary"`
		Detailed        *string `json:"detailed"`
		ConfidenceLevel *string `json:"confidence_level"`
	} `json:"personal_finance_category"`
}

// DecodeTransaction translates one Plaid transaction object into a
// store.Transaction with Raw set to the object itself and ItemID left
// empty. The amount keeps Plaid's sign convention (positive is money out).
func DecodeTransaction(raw json.RawMessage) (store.Transaction, error) {
	var r rawTransaction
	if err := newDecoder(raw).Decode(&r); err != nil {
		return store.Transaction{}, fmt.Errorf("plaid: decode transaction: %w", err)
	}
	if r.TransactionID == "" {
		return store.Transaction{}, fmt.Errorf("plaid: decode transaction: transaction_id is empty")
	}
	wrap := func(err error) error {
		return fmt.Errorf("plaid: decode transaction %s: %w", r.TransactionID, err)
	}
	if r.AccountID == "" || r.Name == "" {
		return store.Transaction{}, wrap(fmt.Errorf("account_id or name is empty"))
	}
	if r.Amount == nil {
		return store.Transaction{}, wrap(fmt.Errorf("amount is missing"))
	}
	if r.Date == nil {
		return store.Transaction{}, wrap(fmt.Errorf("date is missing"))
	}

	var t store.Transaction
	t.TransactionID = r.TransactionID
	t.AccountID = r.AccountID
	amount, err := decodeNumber(r.Amount, "amount")
	if err != nil {
		return store.Transaction{}, wrap(err)
	}
	t.Amount = *amount
	t.ISOCurrencyCode = r.ISOCurrencyCode
	t.UnofficialCurrencyCode = r.UnofficialCurrencyCode

	date, err := decodeDate(r.Date, "date")
	if err != nil {
		return store.Transaction{}, wrap(err)
	}
	t.Date = *date
	if t.AuthorizedDate, err = decodeDate(r.AuthorizedDate, "authorized_date"); err != nil {
		return store.Transaction{}, wrap(err)
	}
	if t.DateTime, err = decodeTime(r.Datetime, "datetime"); err != nil {
		return store.Transaction{}, wrap(err)
	}
	if t.AuthorizedDateTime, err = decodeTime(r.AuthorizedDatetime, "authorized_datetime"); err != nil {
		return store.Transaction{}, wrap(err)
	}

	t.Name = r.Name
	t.MerchantName = r.MerchantName
	t.MerchantEntityID = r.MerchantEntityID
	t.Pending = r.Pending
	t.PendingTransactionID = r.PendingTransactionID
	if r.PersonalFinanceCategory != nil {
		t.PFCPrimary = r.PersonalFinanceCategory.Primary
		t.PFCDetailed = r.PersonalFinanceCategory.Detailed
		t.PFCConfidence = r.PersonalFinanceCategory.ConfidenceLevel
	}
	t.PaymentChannel = r.PaymentChannel
	t.TransactionCode = r.TransactionCode
	t.Raw = compact(raw)
	return t, nil
}

// rawSyncResponse is the shape of a /transactions/sync body with the row
// objects left undecoded.
type rawSyncResponse struct {
	UpdateStatus string            `json:"transactions_update_status"`
	Accounts     []json.RawMessage `json:"accounts"`
	Added        []json.RawMessage `json:"added"`
	Modified     []json.RawMessage `json:"modified"`
	Removed      []struct {
		TransactionID string `json:"transaction_id"`
		AccountID     string `json:"account_id"`
	} `json:"removed"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
	RequestID  string `json:"request_id"`
}

// DecodeSyncPage decodes a complete /transactions/sync response body.
func DecodeSyncPage(body []byte) (*SyncPage, error) {
	var r rawSyncResponse
	if err := newDecoder(body).Decode(&r); err != nil {
		return nil, fmt.Errorf("plaid: decode /transactions/sync: %w", err)
	}
	p := &SyncPage{
		UpdateStatus: r.UpdateStatus,
		NextCursor:   r.NextCursor,
		HasMore:      r.HasMore,
		RequestID:    r.RequestID,
		Accounts:     make([]store.Account, 0, len(r.Accounts)),
		Added:        make([]store.Transaction, 0, len(r.Added)),
		Modified:     make([]store.Transaction, 0, len(r.Modified)),
		Removed:      make([]Removed, 0, len(r.Removed)),
	}
	for _, raw := range r.Accounts {
		a, err := DecodeAccount(raw)
		if err != nil {
			return nil, fmt.Errorf("plaid: decode /transactions/sync: accounts: %w", err)
		}
		p.Accounts = append(p.Accounts, a)
	}
	for _, raw := range r.Added {
		t, err := DecodeTransaction(raw)
		if err != nil {
			return nil, fmt.Errorf("plaid: decode /transactions/sync: added: %w", err)
		}
		p.Added = append(p.Added, t)
	}
	for _, raw := range r.Modified {
		t, err := DecodeTransaction(raw)
		if err != nil {
			return nil, fmt.Errorf("plaid: decode /transactions/sync: modified: %w", err)
		}
		p.Modified = append(p.Modified, t)
	}
	for _, rm := range r.Removed {
		if rm.TransactionID == "" {
			return nil, fmt.Errorf("plaid: decode /transactions/sync: removed: transaction_id is empty")
		}
		p.Removed = append(p.Removed, Removed{TransactionID: rm.TransactionID, AccountID: rm.AccountID})
	}
	return p, nil
}

// DecodeAccountsGet decodes a complete /accounts/get response body.
func DecodeAccountsGet(body []byte) (*Accounts, error) {
	var r struct {
		Accounts  []json.RawMessage `json:"accounts"`
		RequestID string            `json:"request_id"`
	}
	if err := newDecoder(body).Decode(&r); err != nil {
		return nil, fmt.Errorf("plaid: decode /accounts/get: %w", err)
	}
	out := &Accounts{RequestID: r.RequestID, Accounts: make([]store.Account, 0, len(r.Accounts))}
	for _, raw := range r.Accounts {
		a, err := DecodeAccount(raw)
		if err != nil {
			return nil, fmt.Errorf("plaid: decode /accounts/get: %w", err)
		}
		out.Accounts = append(out.Accounts, a)
	}
	return out, nil
}

// rawItem is the subset of a Plaid item object ItemInfo carries in fields.
type rawItem struct {
	ItemID                string    `json:"item_id"`
	InstitutionID         *string   `json:"institution_id"`
	InstitutionName       *string   `json:"institution_name"`
	Webhook               *string   `json:"webhook"`
	ConsentExpirationTime *string   `json:"consent_expiration_time"`
	Products              []string  `json:"products"`
	ConsentedProducts     []string  `json:"consented_products"`
	BilledProducts        []string  `json:"billed_products"`
	AvailableProducts     []string  `json:"available_products"`
	Error                 *rawError `json:"error"`
}

// rawError is a Plaid error object as embedded in responses and webhooks.
type rawError struct {
	Type           string `json:"error_type"`
	Code           string `json:"error_code"`
	Message        string `json:"error_message"`
	DisplayMessage string `json:"display_message"`
	RequestID      string `json:"request_id"`
	Status         int    `json:"status"`
}

// toError converts a raw error object; nil stays nil.
func (r *rawError) toError(endpoint string) *Error {
	if r == nil {
		return nil
	}
	return &Error{
		Endpoint:       endpoint,
		Type:           r.Type,
		Code:           r.Code,
		Message:        r.Message,
		DisplayMessage: r.DisplayMessage,
		RequestID:      r.RequestID,
		HTTPStatus:     r.Status,
	}
}

// DecodeError decodes a Plaid error body (the JSON Plaid returns with a
// 4xx or 5xx) into an *Error for the given endpoint and HTTP status. It
// fails when the body is not a Plaid error object, so callers can fall
// back to a status-only Error.
func DecodeError(body []byte, endpoint string, httpStatus int) (*Error, error) {
	var r rawError
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("plaid: decode error body: %w", err)
	}
	if r.Type == "" && r.Code == "" {
		return nil, fmt.Errorf("plaid: decode error body: not a Plaid error object")
	}
	e := r.toError(endpoint)
	e.HTTPStatus = httpStatus
	return e, nil
}

// DecodeItem translates one Plaid item object (the "item" member of
// /item/get and of several other responses) into an ItemInfo.
func DecodeItem(raw json.RawMessage) (*ItemInfo, error) {
	var r rawItem
	if err := newDecoder(raw).Decode(&r); err != nil {
		return nil, fmt.Errorf("plaid: decode item: %w", err)
	}
	if r.ItemID == "" {
		return nil, fmt.Errorf("plaid: decode item: item_id is empty")
	}
	consent, err := decodeTime(r.ConsentExpirationTime, "consent_expiration_time")
	if err != nil {
		return nil, fmt.Errorf("plaid: decode item %s: %w", r.ItemID, err)
	}
	return &ItemInfo{
		ItemID:            r.ItemID,
		InstitutionID:     r.InstitutionID,
		InstitutionName:   r.InstitutionName,
		Webhook:           r.Webhook,
		ConsentExpiresAt:  consent,
		Products:          r.Products,
		ConsentedProducts: r.ConsentedProducts,
		BilledProducts:    r.BilledProducts,
		AvailableProducts: r.AvailableProducts,
		Error:             r.Error.toError("/item/get"),
		Raw:               compact(raw),
	}, nil
}

// DecodeItemGet decodes a complete /item/get response body.
func DecodeItemGet(body []byte) (*ItemInfo, error) {
	var r struct {
		Item      json.RawMessage `json:"item"`
		RequestID string          `json:"request_id"`
	}
	if err := newDecoder(body).Decode(&r); err != nil {
		return nil, fmt.Errorf("plaid: decode /item/get: %w", err)
	}
	if len(r.Item) == 0 {
		return nil, fmt.Errorf("plaid: decode /item/get: item is missing")
	}
	info, err := DecodeItem(r.Item)
	if err != nil {
		return nil, fmt.Errorf("plaid: decode /item/get: %w", err)
	}
	info.RequestID = r.RequestID
	return info, nil
}

// DecodeVerificationKey decodes a complete /webhook_verification_key/get
// response body.
func DecodeVerificationKey(body []byte) (*VerificationKey, error) {
	var r struct {
		Key struct {
			Alg       string `json:"alg"`
			Crv       string `json:"crv"`
			Kid       string `json:"kid"`
			Kty       string `json:"kty"`
			Use       string `json:"use"`
			X         string `json:"x"`
			Y         string `json:"y"`
			CreatedAt int64  `json:"created_at"`
			ExpiredAt *int64 `json:"expired_at"`
		} `json:"key"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("plaid: decode /webhook_verification_key/get: %w", err)
	}
	if r.Key.Kid == "" || r.Key.X == "" || r.Key.Y == "" {
		return nil, fmt.Errorf("plaid: decode /webhook_verification_key/get: key is incomplete")
	}
	k := &VerificationKey{
		KeyID:     r.Key.Kid,
		Algorithm: r.Key.Alg,
		Curve:     r.Key.Crv,
		KeyType:   r.Key.Kty,
		Use:       r.Key.Use,
		X:         r.Key.X,
		Y:         r.Key.Y,
		CreatedAt: time.Unix(r.Key.CreatedAt, 0).UTC(),
		RequestID: r.RequestID,
	}
	if r.Key.ExpiredAt != nil {
		t := time.Unix(*r.Key.ExpiredAt, 0).UTC()
		k.ExpiredAt = &t
	}
	return k, nil
}

// compact returns raw with insignificant whitespace removed, as an
// independent copy, so a Raw value is stable regardless of how the body
// was formatted and does not alias the response buffer. Invalid JSON
// cannot reach here (it was just decoded), so a failure is returned as
// the input copied verbatim.
func compact(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return json.RawMessage(buf.Bytes())
}
