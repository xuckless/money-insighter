package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"plaidsync/internal/civil"
	"plaidsync/internal/money"
)

// Shared helpers for the items, jobs and runs tests. They are deliberately
// named to avoid clashing with helpers other test files may define.

// optStr returns a pointer to s, for nullable string fields.
func optStr(s string) *string { return &s }

// optTime returns a pointer to t, for nullable timestamp fields.
func optTime(t time.Time) *time.Time { return &t }

// testCredential builds a distinctive credential for tests. The bytes are
// arbitrary: the store never interprets them.
func testCredential(seed string, version uint32) Credential {
	return Credential{
		Ciphertext: []byte("nonce-and-ciphertext-for-" + seed),
		KeyVersion: version,
	}
}

// wantJSONEqual compares two JSON documents by value, not by bytes: JSONB
// normalises key order and whitespace, so what comes back is never
// byte-identical to what went in.
func wantJSONEqual(t *testing.T, what string, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: got is not valid JSON (%v): %s", what, err, got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("%s: want is not valid JSON (%v): %s", what, err, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// wantStr checks a nullable string field against an expected pointer.
func wantStr(t *testing.T, what string, got, want *string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil:
		t.Errorf("%s = nil, want %q", what, *want)
	case want == nil:
		t.Errorf("%s = %q, want nil", what, *got)
	case *got != *want:
		t.Errorf("%s = %q, want %q", what, *got, *want)
	}
}

// wantTime checks a nullable timestamp against an expected pointer, by
// instant (pgx returns timestamptz in the local zone). The expectation is
// truncated to microseconds, which is all Postgres keeps.
func wantTime(t *testing.T, what string, got, want *time.Time) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil:
		t.Errorf("%s = nil, want %v", what, *want)
	case want == nil:
		t.Errorf("%s = %v, want nil", what, *got)
	case !got.Equal(want.Truncate(time.Microsecond)):
		t.Errorf("%s = %v, want %v", what, *got, *want)
	}
}

// wantRecent checks that a database-assigned timestamp is set and was
// taken from a clock that agrees with ours to within a minute.
func wantRecent(t *testing.T, what string, got *time.Time) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want a timestamp", what)
		return
	}
	if d := time.Since(*got); d < -time.Minute || d > time.Minute {
		t.Errorf("%s = %v, which is %v from now", what, *got, d)
	}
}

// rawCredential reads the credential columns straight from the table, so a
// test can prove what is stored rather than what GetCredential says.
func rawCredential(t *testing.T, s *Store, itemID string) (ciphertext []byte, keyVersion *int32) {
	t.Helper()
	err := s.pool.QueryRow(testCtx(t),
		`SELECT encrypted_access_token, key_version FROM plaid_items WHERE item_id = $1`, itemID).
		Scan(&ciphertext, &keyVersion)
	if err != nil {
		t.Fatalf("read credential columns of %q: %v", itemID, err)
	}
	return ciphertext, keyVersion
}

// setCursor writes a cursor directly, standing in for a completed sync so
// tests can prove that item operations keep it.
func setCursor(t *testing.T, s *Store, itemID, cursor string) {
	t.Helper()
	if _, err := s.pool.Exec(testCtx(t), `UPDATE plaid_items SET cursor = $2 WHERE item_id = $1`, itemID, cursor); err != nil {
		t.Fatalf("set cursor of %q: %v", itemID, err)
	}
}

func TestUpsertItemThenGetItem(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	consent := time.Date(2025, time.March, 4, 5, 6, 7, 891000, time.FixedZone("PST", -8*3600))
	n := NewItem{
		ItemID:           "item_get",
		InstitutionID:    optStr("ins_cibc"),
		InstitutionName:  optStr("CIBC"),
		Credential:       testCredential("item_get", 3),
		ConsentExpiresAt: optTime(consent),
		Raw:              json.RawMessage(`{"item": {"item_id": "item_get", "webhook": "https://example.com/hook"}, "b": [1, 2], "a": null}`),
	}
	if err := s.UpsertItem(ctx, n); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	item, err := s.GetItem(ctx, "item_get")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.ItemID != "item_get" {
		t.Errorf("ItemID = %q", item.ItemID)
	}
	wantStr(t, "InstitutionID", item.InstitutionID, n.InstitutionID)
	wantStr(t, "InstitutionName", item.InstitutionName, n.InstitutionName)
	if item.Status != ItemStatusActive {
		t.Errorf("Status = %q, want active", item.Status)
	}
	if item.Cursor != nil {
		t.Errorf("Cursor = %q, want nil on a fresh item", *item.Cursor)
	}
	if item.LastErrorCode != nil || item.LastErrorType != nil || item.LastErrorMessage != nil || item.LastErrorAt != nil {
		t.Errorf("fresh item has last_error_* set: %+v", item)
	}
	if item.LastSuccessfulSyncAt != nil {
		t.Errorf("LastSuccessfulSyncAt = %v, want nil", *item.LastSuccessfulSyncAt)
	}
	wantTime(t, "ConsentExpiresAt", item.ConsentExpiresAt, &consent)
	wantJSONEqual(t, "Raw", item.Raw, n.Raw)
	wantRecent(t, "CreatedAt", &item.CreatedAt)
	wantRecent(t, "UpdatedAt", &item.UpdatedAt)
}

func TestGetItemNotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	item, err := s.GetItem(ctx, "item_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetItem(unknown) = %v, %v; want ErrNotFound", item, err)
	}
	if item != nil {
		t.Errorf("GetItem(unknown) returned a non-nil item with an error")
	}
}

func TestGetCredentialReturnsWhatWasStored(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	// A blob with every byte value, to prove BYTEA is not text-mangled.
	blob := make([]byte, 256)
	for i := range blob {
		blob[i] = byte(i)
	}
	n := NewItem{ItemID: "item_cred", Credential: Credential{Ciphertext: blob, KeyVersion: 7}}
	if err := s.UpsertItem(ctx, n); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	c, err := s.GetCredential(ctx, "item_cred")
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if !bytes.Equal(c.Ciphertext, blob) {
		t.Errorf("Ciphertext round trip changed the bytes: got %d bytes %x", len(c.Ciphertext), c.Ciphertext)
	}
	if c.KeyVersion != 7 {
		t.Errorf("KeyVersion = %d, want 7", c.KeyVersion)
	}

	if _, err := s.GetCredential(ctx, "item_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetCredential(unknown) = %v, want ErrNotFound", err)
	}
}

func TestUpsertItemAgainReplacesCredentialAndResetsStatus(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	first := NewItem{
		ItemID:          "item_relink",
		InstitutionID:   optStr("ins_1"),
		InstitutionName: optStr("First Bank"),
		Credential:      testCredential("first", 1),
		Raw:             json.RawMessage(`{"v": 1}`),
	}
	if err := s.UpsertItem(ctx, first); err != nil {
		t.Fatalf("UpsertItem first: %v", err)
	}
	setCursor(t, s, "item_relink", "cursor-after-sync-1")
	if err := s.SetItemStatus(ctx, "item_relink", ItemStatusLoginRequired, &ItemError{
		Code: "ITEM_LOGIN_REQUIRED", Type: "ITEM_ERROR", Message: "the login details of this item have changed",
	}); err != nil {
		t.Fatalf("SetItemStatus: %v", err)
	}

	second := NewItem{
		ItemID:           "item_relink",
		InstitutionID:    optStr("ins_1"),
		InstitutionName:  optStr("First Bank (renamed)"),
		Credential:       testCredential("second", 2),
		ConsentExpiresAt: optTime(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)),
		Raw:              json.RawMessage(`{"v": 2}`),
	}
	if err := s.UpsertItem(ctx, second); err != nil {
		t.Fatalf("UpsertItem second: %v", err)
	}

	c, err := s.GetCredential(ctx, "item_relink")
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if !bytes.Equal(c.Ciphertext, second.Credential.Ciphertext) || c.KeyVersion != 2 {
		t.Errorf("credential after re-upsert = %q v%d, want %q v2", c.Ciphertext, c.KeyVersion, second.Credential.Ciphertext)
	}

	item, err := s.GetItem(ctx, "item_relink")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Status != ItemStatusActive {
		t.Errorf("Status = %q, want active", item.Status)
	}
	if item.LastErrorCode != nil || item.LastErrorType != nil || item.LastErrorMessage != nil || item.LastErrorAt != nil {
		t.Errorf("last_error_* not cleared: code=%v type=%v msg=%v at=%v",
			item.LastErrorCode, item.LastErrorType, item.LastErrorMessage, item.LastErrorAt)
	}
	wantStr(t, "Cursor (kept across re-link)", item.Cursor, optStr("cursor-after-sync-1"))
	wantStr(t, "InstitutionName", item.InstitutionName, second.InstitutionName)
	wantTime(t, "ConsentExpiresAt", item.ConsentExpiresAt, second.ConsentExpiresAt)
	wantJSONEqual(t, "Raw", item.Raw, second.Raw)

	// Only one row exists.
	items, err := s.ListItems(ctx)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("ListItems has %d items, want 1", len(items))
	}
}

// TestUpsertItemNilFieldsKeepStoredValues pins the COALESCE behaviour: a
// re-link that does not know the institution or has no /item/get payload
// must not wipe what an earlier link recorded.
func TestUpsertItemNilFieldsKeepStoredValues(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.UpsertItem(ctx, NewItem{
		ItemID:          "item_keep",
		InstitutionID:   optStr("ins_keep"),
		InstitutionName: optStr("Keep Me"),
		Credential:      testCredential("keep-1", 1),
		Raw:             json.RawMessage(`{"kept": true}`),
	}); err != nil {
		t.Fatalf("UpsertItem first: %v", err)
	}
	if err := s.UpsertItem(ctx, NewItem{
		ItemID:     "item_keep",
		Credential: testCredential("keep-2", 1),
	}); err != nil {
		t.Fatalf("UpsertItem second: %v", err)
	}

	item, err := s.GetItem(ctx, "item_keep")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	wantStr(t, "InstitutionID", item.InstitutionID, optStr("ins_keep"))
	wantStr(t, "InstitutionName", item.InstitutionName, optStr("Keep Me"))
	wantJSONEqual(t, "Raw", item.Raw, json.RawMessage(`{"kept": true}`))

	// And a fresh insert with nil Raw stores SQL NULL, which reads back as
	// a nil RawMessage rather than the JSON literal null.
	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_noraw", Credential: testCredential("noraw", 1)}); err != nil {
		t.Fatalf("UpsertItem no raw: %v", err)
	}
	item, err = s.GetItem(ctx, "item_noraw")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Raw != nil {
		t.Errorf("Raw = %s, want nil for a NULL column", item.Raw)
	}
	var rawIsNull bool
	if err := s.pool.QueryRow(ctx, `SELECT raw IS NULL FROM plaid_items WHERE item_id = 'item_noraw'`).Scan(&rawIsNull); err != nil {
		t.Fatalf("read raw IS NULL: %v", err)
	}
	if !rawIsNull {
		t.Error("nil json.RawMessage was stored as a JSON value, want SQL NULL")
	}
}

func TestUpsertItemRejectsBadInput(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	cases := map[string]NewItem{
		"empty item id":    {ItemID: "", Credential: testCredential("x", 1)},
		"empty ciphertext": {ItemID: "item_bad", Credential: Credential{Ciphertext: nil, KeyVersion: 1}},
		"zero key version": {ItemID: "item_bad", Credential: Credential{Ciphertext: []byte("x"), KeyVersion: 0}},
		"invalid json raw": {ItemID: "item_bad", Credential: testCredential("x", 1), Raw: json.RawMessage(`{not json`)},
	}
	for name, n := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.UpsertItem(ctx, n); err == nil {
				t.Fatal("UpsertItem succeeded, want error")
			}
		})
	}
	items, err := s.ListItems(ctx)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("rejected upserts left %d rows behind", len(items))
	}
}

func TestMarkItemRemoved(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_rm", Credential: testCredential("rm", 1)}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	setCursor(t, s, "item_rm", "cursor-before-removal")

	if err := s.MarkItemRemoved(ctx, "item_rm"); err != nil {
		t.Fatalf("MarkItemRemoved: %v", err)
	}

	ciphertext, keyVersion := rawCredential(t, s, "item_rm")
	if ciphertext != nil {
		t.Errorf("encrypted_access_token after removal = %q, want NULL", ciphertext)
	}
	if keyVersion != nil {
		t.Errorf("key_version after removal = %d, want NULL", *keyVersion)
	}
	if _, err := s.GetCredential(ctx, "item_rm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetCredential(removed) = %v, want ErrNotFound", err)
	}

	item, err := s.GetItem(ctx, "item_rm")
	if err != nil {
		t.Fatalf("GetItem(removed): %v", err)
	}
	if item.Status != ItemStatusRemoved {
		t.Errorf("Status = %q, want removed", item.Status)
	}
	wantStr(t, "Cursor (kept for history)", item.Cursor, optStr("cursor-before-removal"))

	// Idempotent.
	if err := s.MarkItemRemoved(ctx, "item_rm"); err != nil {
		t.Errorf("second MarkItemRemoved: %v, want nil", err)
	}
	// Unknown item.
	if err := s.MarkItemRemoved(ctx, "item_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkItemRemoved(unknown) = %v, want ErrNotFound", err)
	}
	// A removed item has no credential to rotate and no lifecycle left.
	if err := s.UpdateCredential(ctx, "item_rm", testCredential("rotated", 2)); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateCredential(removed) = %v, want ErrNotFound", err)
	}
	if err := s.SetItemStatus(ctx, "item_rm", ItemStatusActive, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetItemStatus(removed) = %v, want ErrNotFound", err)
	}
	if ciphertext, _ := rawCredential(t, s, "item_rm"); ciphertext != nil {
		t.Errorf("credential came back after rejected calls: %q", ciphertext)
	}
}

func TestUpsertItemRevivesRemovedItem(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_revive", Credential: testCredential("old", 1)}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	setCursor(t, s, "item_revive", "cursor-kept")
	if err := s.MarkItemRemoved(ctx, "item_revive"); err != nil {
		t.Fatalf("MarkItemRemoved: %v", err)
	}

	revived := NewItem{ItemID: "item_revive", Credential: testCredential("new", 2), InstitutionName: optStr("Back Again")}
	if err := s.UpsertItem(ctx, revived); err != nil {
		t.Fatalf("UpsertItem(removed item): %v", err)
	}

	item, err := s.GetItem(ctx, "item_revive")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Status != ItemStatusActive {
		t.Errorf("Status = %q, want active", item.Status)
	}
	wantStr(t, "InstitutionName", item.InstitutionName, optStr("Back Again"))
	wantStr(t, "Cursor", item.Cursor, optStr("cursor-kept"))

	c, err := s.GetCredential(ctx, "item_revive")
	if err != nil {
		t.Fatalf("GetCredential(revived): %v", err)
	}
	if !bytes.Equal(c.Ciphertext, revived.Credential.Ciphertext) || c.KeyVersion != 2 {
		t.Errorf("credential = %q v%d, want %q v2", c.Ciphertext, c.KeyVersion, revived.Credential.Ciphertext)
	}
}

func TestUpdateCredential(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_rot", Credential: testCredential("v1", 1), InstitutionName: optStr("Bank")}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if err := s.SetItemStatus(ctx, "item_rot", ItemStatusError, &ItemError{Code: "X", Type: "Y", Message: "z"}); err != nil {
		t.Fatalf("SetItemStatus: %v", err)
	}
	before, err := s.GetItem(ctx, "item_rot")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}

	rotated := testCredential("v2", 2)
	if err := s.UpdateCredential(ctx, "item_rot", rotated); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	c, err := s.GetCredential(ctx, "item_rot")
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if !bytes.Equal(c.Ciphertext, rotated.Ciphertext) || c.KeyVersion != 2 {
		t.Errorf("credential = %q v%d, want %q v2", c.Ciphertext, c.KeyVersion, rotated.Ciphertext)
	}

	// Nothing but the credential changed.
	after, err := s.GetItem(ctx, "item_rot")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if after.Status != before.Status {
		t.Errorf("Status changed from %q to %q", before.Status, after.Status)
	}
	wantStr(t, "LastErrorCode", after.LastErrorCode, before.LastErrorCode)
	wantStr(t, "InstitutionName", after.InstitutionName, before.InstitutionName)

	if err := s.UpdateCredential(ctx, "item_missing", rotated); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateCredential(unknown) = %v, want ErrNotFound", err)
	}
	if err := s.UpdateCredential(ctx, "item_rot", Credential{}); err == nil {
		t.Error("UpdateCredential with an empty credential succeeded, want error")
	}
}

func TestSetItemStatus(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_status", Credential: testCredential("s", 1)}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	// With an ItemError carrying an explicit timestamp.
	at := time.Date(2025, time.June, 7, 8, 9, 10, 123456000, time.UTC)
	e := &ItemError{Code: "INSTITUTION_DOWN", Type: "INSTITUTION_ERROR", Message: "the institution is down", At: at}
	if err := s.SetItemStatus(ctx, "item_status", ItemStatusError, e); err != nil {
		t.Fatalf("SetItemStatus(error, e): %v", err)
	}
	item, err := s.GetItem(ctx, "item_status")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Status != ItemStatusError {
		t.Errorf("Status = %q, want error", item.Status)
	}
	wantStr(t, "LastErrorCode", item.LastErrorCode, optStr(e.Code))
	wantStr(t, "LastErrorType", item.LastErrorType, optStr(e.Type))
	wantStr(t, "LastErrorMessage", item.LastErrorMessage, optStr(e.Message))
	wantTime(t, "LastErrorAt", item.LastErrorAt, &at)

	// Without an ItemError: status changes, last_error_* untouched.
	if err := s.SetItemStatus(ctx, "item_status", ItemStatusActive, nil); err != nil {
		t.Fatalf("SetItemStatus(active, nil): %v", err)
	}
	item, err = s.GetItem(ctx, "item_status")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Status != ItemStatusActive {
		t.Errorf("Status = %q, want active", item.Status)
	}
	wantStr(t, "LastErrorCode (untouched)", item.LastErrorCode, optStr(e.Code))
	wantTime(t, "LastErrorAt (untouched)", item.LastErrorAt, &at)

	// With an ItemError whose At is zero: the database clock is used.
	if err := s.SetItemStatus(ctx, "item_status", ItemStatusPendingExpiration, &ItemError{Code: "PENDING_EXPIRATION", Type: "ITEM_ERROR", Message: "consent expires soon"}); err != nil {
		t.Fatalf("SetItemStatus(pending_expiration): %v", err)
	}
	item, err = s.GetItem(ctx, "item_status")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Status != ItemStatusPendingExpiration {
		t.Errorf("Status = %q, want pending_expiration", item.Status)
	}
	wantStr(t, "LastErrorCode", item.LastErrorCode, optStr("PENDING_EXPIRATION"))
	wantRecent(t, "LastErrorAt (defaulted)", item.LastErrorAt)

	// Every non-removed status is accepted.
	for _, st := range []ItemStatus{ItemStatusLoginRequired, ItemStatusPermissionRevoked, ItemStatusError, ItemStatusActive} {
		if err := s.SetItemStatus(ctx, "item_status", st, nil); err != nil {
			t.Errorf("SetItemStatus(%q): %v", st, err)
		}
	}

	// Rejections.
	if err := s.SetItemStatus(ctx, "item_status", ItemStatus("bogus"), nil); err == nil {
		t.Error("SetItemStatus(bogus) succeeded, want error")
	}
	if err := s.SetItemStatus(ctx, "item_status", ItemStatusRemoved, nil); err == nil {
		t.Error("SetItemStatus(removed) succeeded, want error (use MarkItemRemoved)")
	}
	if err := s.SetItemStatus(ctx, "item_missing", ItemStatusActive, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetItemStatus(unknown) = %v, want ErrNotFound", err)
	}
	if err := s.SetItemStatus(ctx, "item_missing", ItemStatusError, e); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetItemStatus(unknown, e) = %v, want ErrNotFound", err)
	}
}

func TestListItemsOrderAndStatuses(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	empty, err := s.ListItems(ctx)
	if err != nil {
		t.Fatalf("ListItems on empty table: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("ListItems on empty table = %#v, want empty non-nil slice", empty)
	}

	// Insert in one order, then backdate created_at so the expected order
	// differs from insertion order and from item_id order.
	for _, id := range []string{"item_b", "item_a", "item_c"} {
		if err := s.UpsertItem(ctx, NewItem{ItemID: id, Credential: testCredential(id, 1)}); err != nil {
			t.Fatalf("UpsertItem(%s): %v", id, err)
		}
	}
	for id, age := range map[string]string{"item_c": "3 days", "item_a": "2 days", "item_b": "1 day"} {
		if _, err := s.pool.Exec(ctx, `UPDATE plaid_items SET created_at = now() - $2::interval WHERE item_id = $1`, id, age); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}
	if err := s.MarkItemRemoved(ctx, "item_a"); err != nil {
		t.Fatalf("MarkItemRemoved: %v", err)
	}
	if err := s.SetItemStatus(ctx, "item_b", ItemStatusLoginRequired, nil); err != nil {
		t.Fatalf("SetItemStatus: %v", err)
	}

	items, err := s.ListItems(ctx)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	var ids []string
	statuses := map[string]ItemStatus{}
	for _, it := range items {
		ids = append(ids, it.ItemID)
		statuses[it.ItemID] = it.Status
	}
	if want := []string{"item_c", "item_a", "item_b"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ListItems order = %v, want %v (by created_at)", ids, want)
	}
	if statuses["item_a"] != ItemStatusRemoved || statuses["item_b"] != ItemStatusLoginRequired || statuses["item_c"] != ItemStatusActive {
		t.Errorf("statuses = %v", statuses)
	}
}

// TestItemCredentialConstraintsEnforcedByDatabase proves that the
// invariants the Go code relies on are held by the schema itself: a
// direct UPDATE that breaks "removed if and only if credential is NULL", or
// that leaves a credential without a key version, is rejected with a check
// violation rather than accepted.
func TestItemCredentialConstraintsEnforcedByDatabase(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_inv", Credential: testCredential("inv", 1)}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if err := s.UpsertItem(ctx, NewItem{ItemID: "item_inv_removed", Credential: testCredential("inv2", 1)}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if err := s.MarkItemRemoved(ctx, "item_inv_removed"); err != nil {
		t.Fatalf("MarkItemRemoved: %v", err)
	}

	violations := []struct {
		name, sql, itemID, constraint string
	}{
		{"active item losing its credential", `UPDATE plaid_items SET encrypted_access_token = NULL, key_version = NULL WHERE item_id = $1`, "item_inv", "plaid_items_credential_check"},
		{"removed status while keeping the credential", `UPDATE plaid_items SET status = 'removed' WHERE item_id = $1`, "item_inv", "plaid_items_credential_check"},
		{"removed item given a credential back", `UPDATE plaid_items SET encrypted_access_token = '\x01'::bytea, key_version = 1 WHERE item_id = $1`, "item_inv_removed", "plaid_items_credential_check"},
		{"removed item made active without a credential", `UPDATE plaid_items SET status = 'active' WHERE item_id = $1`, "item_inv_removed", "plaid_items_credential_check"},
		{"credential without key_version", `UPDATE plaid_items SET key_version = NULL WHERE item_id = $1`, "item_inv", "plaid_items_key_version_check"},
		{"status outside the enum", `UPDATE plaid_items SET status = 'paused' WHERE item_id = $1`, "item_inv", "plaid_items_status_check"},
	}
	for _, v := range violations {
		t.Run(v.name, func(t *testing.T) {
			_, err := s.pool.Exec(ctx, v.sql, v.itemID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("got %v, want a check violation", err)
			}
			if pgErr.Code != "23514" {
				t.Errorf("SQLSTATE = %s, want 23514 (check_violation)", pgErr.Code)
			}
			if pgErr.ConstraintName != v.constraint {
				t.Errorf("constraint = %q, want %q", pgErr.ConstraintName, v.constraint)
			}
		})
	}

	// After all that, the rows are exactly as the store left them.
	if c, err := s.GetCredential(ctx, "item_inv"); err != nil || c.KeyVersion != 1 {
		t.Errorf("GetCredential(item_inv) after rejected updates = %v, %v", c, err)
	}
	if _, err := s.GetCredential(ctx, "item_inv_removed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetCredential(item_inv_removed) = %v, want ErrNotFound", err)
	}
}

// setSessionTimeZone makes every pooled connection of s run with the given
// session time zone, so a date or timestamp that were routed through a
// local-time conversion would come back shifted. It sets the zone as a
// database default and then drops every existing connection so the next
// acquisition picks it up.
func setSessionTimeZone(t *testing.T, s *Store, zone string) {
	t.Helper()
	ctx := testCtx(t)
	var db string
	if err := s.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER DATABASE `+quoteIdent(db)+` SET timezone TO '`+zone+`'`); err != nil {
		t.Fatalf("set database time zone: %v", err)
	}
	s.pool.Reset()
	var got string
	if err := s.pool.QueryRow(ctx, `SHOW timezone`).Scan(&got); err != nil {
		t.Fatalf("SHOW timezone: %v", err)
	}
	if got != zone {
		t.Fatalf("session timezone = %q, want %q", got, zone)
	}
}

// insertTestAccount writes an account with raw SQL, passing money.Amount
// values as parameters exactly the way the store's own write paths do, so
// the NUMERIC encode direction is exercised as well as the decode.
func insertTestAccount(t *testing.T, s *Store, a Account) {
	t.Helper()
	_, err := s.pool.Exec(testCtx(t), `
		INSERT INTO plaid_accounts (
			account_id, item_id, name, official_name, mask, type, subtype,
			current_balance, available_balance, credit_limit,
			iso_currency_code, unofficial_currency_code, balance_last_updated_at, raw
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		a.AccountID, a.ItemID, a.Name, a.OfficialName, a.Mask, a.Type, a.Subtype,
		a.CurrentBalance, a.AvailableBalance, a.CreditLimit,
		a.ISOCurrencyCode, a.UnofficialCurrencyCode, a.BalanceLastUpdatedAt, a.Raw)
	if err != nil {
		t.Fatalf("insert account %q: %v", a.AccountID, err)
	}
}

// insertTestTransaction writes a transaction with raw SQL, passing
// money.Amount and civil.Date values as parameters.
func insertTestTransaction(t *testing.T, s *Store, tx Transaction) {
	t.Helper()
	_, err := s.pool.Exec(testCtx(t), `
		INSERT INTO transactions (
			transaction_id, account_id, item_id, amount,
			iso_currency_code, unofficial_currency_code,
			date, authorized_date, datetime, authorized_datetime,
			name, merchant_name, merchant_entity_id,
			pending, pending_transaction_id,
			pfc_primary, pfc_detailed, pfc_confidence, payment_channel, transaction_code,
			raw
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21
		)`,
		tx.TransactionID, tx.AccountID, tx.ItemID, tx.Amount,
		tx.ISOCurrencyCode, tx.UnofficialCurrencyCode,
		tx.Date, tx.AuthorizedDate, tx.DateTime, tx.AuthorizedDateTime,
		tx.Name, tx.MerchantName, tx.MerchantEntityID,
		tx.Pending, tx.PendingTransactionID,
		tx.PFCPrimary, tx.PFCDetailed, tx.PFCConfidence, tx.PaymentChannel, tx.TransactionCode,
		tx.Raw)
	if err != nil {
		t.Fatalf("insert transaction %q: %v", tx.TransactionID, err)
	}
}

// wantAmount checks a nullable NUMERIC field.
func wantAmount(t *testing.T, what string, got *money.Amount, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Errorf("%s = %s, want nil", what, got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s = nil, want %s", what, want)
		return
	}
	if *got != money.MustParse(want) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// TestListAccountsDecodesEveryColumnType seeds accounts directly and reads
// them through ListAccounts, proving NUMERIC(14,2) -> *money.Amount (with
// NULL -> nil), TIMESTAMPTZ instants, JSONB and ordering.
func TestListAccountsDecodesEveryColumnType(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_acc")
	seedItem(t, s, "item_other")

	balanceAt := time.Date(2024, time.March, 31, 23, 30, 0, 250000000, time.FixedZone("PDT", -7*3600))
	full := Account{
		AccountID:              "acc_b",
		ItemID:                 "item_acc",
		Name:                   "Aventura Visa",
		OfficialName:           optStr("CIBC Aventura Visa Infinite"),
		Mask:                   optStr("1234"),
		Type:                   "credit",
		Subtype:                optStr("credit card"),
		CurrentBalance:         amountPtr("9999999999.99"),
		AvailableBalance:       amountPtr("-0.01"),
		CreditLimit:            amountPtr("1500"),
		ISOCurrencyCode:        optStr("CAD"),
		UnofficialCurrencyCode: nil,
		BalanceLastUpdatedAt:   optTime(balanceAt),
		Raw:                    json.RawMessage(`{"account_id": "acc_b", "balances": {"current": 9999999999.99, "limit": 1500}}`),
	}
	sparse := Account{
		AccountID: "acc_a",
		ItemID:    "item_acc",
		Name:      "Chequing",
		Type:      "depository",
		Raw:       json.RawMessage(`{"account_id": "acc_a"}`),
	}
	other := Account{AccountID: "acc_z", ItemID: "item_other", Name: "Other", Type: "loan", Raw: json.RawMessage(`{}`)}
	for _, a := range []Account{full, sparse, other} {
		insertTestAccount(t, s, a)
	}

	accounts, err := s.ListAccounts(ctx, "item_acc")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("ListAccounts returned %d accounts, want 2", len(accounts))
	}
	if accounts[0].AccountID != "acc_a" || accounts[1].AccountID != "acc_b" {
		t.Errorf("order = %s, %s; want acc_a, acc_b", accounts[0].AccountID, accounts[1].AccountID)
	}

	got := accounts[1]
	if got.ItemID != "item_acc" || got.Name != full.Name || got.Type != full.Type {
		t.Errorf("scalar columns = %q %q %q", got.ItemID, got.Name, got.Type)
	}
	wantStr(t, "OfficialName", got.OfficialName, full.OfficialName)
	wantStr(t, "Mask", got.Mask, full.Mask)
	wantStr(t, "Subtype", got.Subtype, full.Subtype)
	wantAmount(t, "CurrentBalance", got.CurrentBalance, "9999999999.99")
	wantAmount(t, "AvailableBalance", got.AvailableBalance, "-0.01")
	wantAmount(t, "CreditLimit", got.CreditLimit, "1500")
	wantStr(t, "ISOCurrencyCode", got.ISOCurrencyCode, optStr("CAD"))
	wantStr(t, "UnofficialCurrencyCode", got.UnofficialCurrencyCode, nil)
	wantTime(t, "BalanceLastUpdatedAt", got.BalanceLastUpdatedAt, &balanceAt)
	wantJSONEqual(t, "Raw", got.Raw, full.Raw)
	wantRecent(t, "FirstSeenAt", &got.FirstSeenAt)
	wantRecent(t, "LastSeenAt", &got.LastSeenAt)
	wantRecent(t, "UpdatedAt", &got.UpdatedAt)
	if got.MissingSince != nil {
		t.Errorf("MissingSince = %v, want nil", *got.MissingSince)
	}

	sp := accounts[0]
	wantAmount(t, "sparse CurrentBalance", sp.CurrentBalance, "")
	wantAmount(t, "sparse AvailableBalance", sp.AvailableBalance, "")
	wantAmount(t, "sparse CreditLimit", sp.CreditLimit, "")
	if sp.OfficialName != nil || sp.Mask != nil || sp.Subtype != nil || sp.BalanceLastUpdatedAt != nil {
		t.Errorf("sparse account has non-nil optional fields: %+v", sp)
	}

	// The stored NUMERIC is exactly what was sent, with the column's scale.
	var asText string
	if err := s.pool.QueryRow(ctx, `SELECT current_balance::text FROM plaid_accounts WHERE account_id = 'acc_b'`).Scan(&asText); err != nil {
		t.Fatalf("read current_balance::text: %v", err)
	}
	if asText != "9999999999.99" {
		t.Errorf("current_balance stored as %q, want 9999999999.99", asText)
	}

	none, err := s.ListAccounts(ctx, "item_unknown")
	if err != nil {
		t.Fatalf("ListAccounts(unknown): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("ListAccounts(unknown) = %#v, want empty non-nil slice", none)
	}
}

// amountPtr parses s into a *money.Amount for nullable fields.
func amountPtr(s string) *money.Amount {
	a := money.MustParse(s)
	return &a
}

// datePtr parses s into a *civil.Date for nullable fields.
func datePtr(s string) *civil.Date {
	d := civil.MustParseDate(s)
	return &d
}

// TestTransactionReadsDecodeEveryColumnType seeds transactions directly and
// reads them back through GetTransaction and ListTransactions. The session
// time zone is a negative-offset zone for the whole test, so a DATE that
// went through any local-time conversion would come back a day early, and
// TIMESTAMPTZ values must still compare equal as instants.
func TestTransactionReadsDecodeEveryColumnType(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	setSessionTimeZone(t, s, "America/Vancouver")
	seedItem(t, s, "item_tx")
	insertTestAccount(t, s, Account{AccountID: "acc_tx", ItemID: "item_tx", Name: "Card", Type: "credit", Raw: json.RawMessage(`{}`), ISOCurrencyCode: optStr("CAD")})

	// 23:30 Vancouver time on the 31st is 06:30 UTC on April 1st; the DATE
	// column must not be affected by either.
	dt := time.Date(2024, time.March, 31, 23, 30, 0, 123456000, time.FixedZone("PDT", -7*3600))
	authDT := time.Date(2024, time.March, 30, 1, 2, 3, 0, time.UTC)
	full := Transaction{
		TransactionID:          "txn_full",
		AccountID:              "acc_tx",
		ItemID:                 "item_tx",
		Amount:                 money.MustParse("12.34"),
		ISOCurrencyCode:        optStr("USD"),
		UnofficialCurrencyCode: nil,
		Date:                   civil.MustParseDate("2024-03-31"),
		AuthorizedDate:         datePtr("2024-03-30"),
		DateTime:               optTime(dt),
		AuthorizedDateTime:     optTime(authDT),
		Name:                   "NETFLIX.COM",
		MerchantName:           optStr("Netflix"),
		MerchantEntityID:       optStr("ent_netflix"),
		Pending:                false,
		PendingTransactionID:   optStr("txn_pending_old"),
		PFCPrimary:             optStr("ENTERTAINMENT"),
		PFCDetailed:            optStr("ENTERTAINMENT_TV_AND_MOVIES"),
		PFCConfidence:          optStr("VERY_HIGH"),
		PaymentChannel:         optStr("online"),
		TransactionCode:        optStr("purchase"),
		Raw:                    json.RawMessage(`{"transaction_id": "txn_full", "amount": 12.34, "date": "2024-03-31"}`),
	}
	insertTestTransaction(t, s, full)

	// Amount fidelity cases, all pending, sharing a later date so ordering
	// is exercised too.
	for i, amt := range []string{"-0.01", "1500", "9999999999.99", "0"} {
		insertTestTransaction(t, s, Transaction{
			TransactionID: "txn_amt_" + string(rune('a'+i)),
			AccountID:     "acc_tx",
			ItemID:        "item_tx",
			Amount:        money.MustParse(amt),
			Date:          civil.MustParseDate("2024-04-01"),
			Name:          "amount " + amt,
			Pending:       true,
			Raw:           json.RawMessage(`{"amount": ` + amt + `}`),
		})
	}

	got, err := s.GetTransaction(ctx, "txn_full")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if got.TransactionID != "txn_full" || got.AccountID != "acc_tx" || got.ItemID != "item_tx" || got.Name != full.Name || got.Pending != false {
		t.Errorf("scalar columns: %+v", got)
	}
	if got.Amount != full.Amount {
		t.Errorf("Amount = %s, want %s", got.Amount, full.Amount)
	}
	wantStr(t, "ISOCurrencyCode", got.ISOCurrencyCode, optStr("USD"))
	wantStr(t, "UnofficialCurrencyCode", got.UnofficialCurrencyCode, nil)
	if got.Date != civil.MustParseDate("2024-03-31") {
		t.Errorf("Date = %v, want 2024-03-31 (session zone America/Vancouver)", got.Date)
	}
	if got.AuthorizedDate == nil || *got.AuthorizedDate != civil.MustParseDate("2024-03-30") {
		t.Errorf("AuthorizedDate = %v, want 2024-03-30", got.AuthorizedDate)
	}
	wantTime(t, "DateTime", got.DateTime, &dt)
	wantTime(t, "AuthorizedDateTime", got.AuthorizedDateTime, &authDT)
	wantStr(t, "MerchantName", got.MerchantName, full.MerchantName)
	wantStr(t, "MerchantEntityID", got.MerchantEntityID, full.MerchantEntityID)
	wantStr(t, "PendingTransactionID", got.PendingTransactionID, full.PendingTransactionID)
	wantStr(t, "PFCPrimary", got.PFCPrimary, full.PFCPrimary)
	wantStr(t, "PFCDetailed", got.PFCDetailed, full.PFCDetailed)
	wantStr(t, "PFCConfidence", got.PFCConfidence, full.PFCConfidence)
	wantStr(t, "PaymentChannel", got.PaymentChannel, full.PaymentChannel)
	wantStr(t, "TransactionCode", got.TransactionCode, full.TransactionCode)
	wantJSONEqual(t, "Raw", got.Raw, full.Raw)
	if got.SupersededBy != nil || got.SupersededAt != nil || got.RemovedAt != nil {
		t.Errorf("read-only link/removal fields set on a fresh row: %+v", got)
	}
	wantRecent(t, "FirstSeenAt", &got.FirstSeenAt)
	wantRecent(t, "UpdatedAt", &got.UpdatedAt)

	// What Postgres itself holds, as text, so the round trip is not just
	// symmetric mangling.
	var dateText, amountText string
	if err := s.pool.QueryRow(ctx, `SELECT date::text, amount::text FROM transactions WHERE transaction_id = 'txn_full'`).Scan(&dateText, &amountText); err != nil {
		t.Fatalf("read text forms: %v", err)
	}
	if dateText != "2024-03-31" {
		t.Errorf("date stored as %q, want 2024-03-31", dateText)
	}
	if amountText != "12.34" {
		t.Errorf("amount stored as %q, want 12.34", amountText)
	}

	// ListTransactions: date DESC, then transaction_id; every row present.
	list, err := s.ListTransactions(ctx, "item_tx", 100, 0)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	var ids []string
	for _, tx := range list {
		ids = append(ids, tx.TransactionID)
	}
	want := []string{"txn_amt_a", "txn_amt_b", "txn_amt_c", "txn_amt_d", "txn_full"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ListTransactions order = %v, want %v", ids, want)
	}
	amounts := map[string]string{}
	for _, tx := range list {
		amounts[tx.TransactionID] = tx.Amount.String()
	}
	for id, wantAmt := range map[string]string{"txn_amt_a": "-0.01", "txn_amt_b": "1500", "txn_amt_c": "9999999999.99", "txn_amt_d": "0", "txn_full": "12.34"} {
		if amounts[id] != wantAmt {
			t.Errorf("%s Amount = %s, want %s", id, amounts[id], wantAmt)
		}
	}
	for _, tx := range list[:4] {
		if tx.AuthorizedDate != nil || tx.DateTime != nil || tx.AuthorizedDateTime != nil {
			t.Errorf("%s: NULL date/timestamps decoded as non-nil: %+v", tx.TransactionID, tx)
		}
		if !tx.Pending {
			t.Errorf("%s: Pending = false, want true", tx.TransactionID)
		}
	}

	// Paging.
	page, err := s.ListTransactions(ctx, "item_tx", 2, 3)
	if err != nil {
		t.Fatalf("ListTransactions(2, 3): %v", err)
	}
	if len(page) != 2 || page[0].TransactionID != "txn_amt_d" || page[1].TransactionID != "txn_full" {
		t.Errorf("ListTransactions(2, 3) = %v", page)
	}
	for _, bad := range [][2]int{{0, 0}, {-1, 0}, {1001, 0}, {10, -1}} {
		if _, err := s.ListTransactions(ctx, "item_tx", bad[0], bad[1]); err == nil {
			t.Errorf("ListTransactions(limit=%d, offset=%d) succeeded, want error", bad[0], bad[1])
		}
	}
	none, err := s.ListTransactions(ctx, "item_unknown", 10, 0)
	if err != nil {
		t.Fatalf("ListTransactions(unknown): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("ListTransactions(unknown) = %#v, want empty non-nil slice", none)
	}

	if _, err := s.GetTransaction(ctx, "txn_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTransaction(unknown) = %v, want ErrNotFound", err)
	}

	// An invalid civil.Date cannot be stored: Value() refuses it before the
	// database sees anything.
	if _, err := s.pool.Exec(ctx, `SELECT $1::date`, civil.Date{}); err == nil {
		t.Error("zero civil.Date was accepted as a DATE parameter, want error")
	}
}
