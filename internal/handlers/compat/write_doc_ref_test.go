package compat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/models"
)

// --- minimal pgx.Tx fake for doc_ref tests ---

// docRefFakeTx implements pgx.Tx with only the methods exercised by updatePurchaseOrderDocRef.
// All unimplemented methods panic so tests fail loudly if code paths change.
type docRefFakeTx struct {
	// scanRows controls QueryRow results in sequence.
	scanRows []docRefScanRow
	rowIdx   int
	// execCalls captures UPDATE statements in order.
	execCalls []docRefExecCall
	execErr   error
	committed bool
	rolledBack bool
}

type docRefScanRow struct {
	docRef  string
	remark5 string
	err     error
	// For the "other trans_flag" lookup: scanOneInt > 0 means return that int.
	scanOneInt *int
}

type docRefExecCall struct {
	sql  string
	args []any
}

func (t *docRefFakeTx) Begin(ctx context.Context) (pgx.Tx, error) { panic("not implemented") }
func (t *docRefFakeTx) Commit(ctx context.Context) error {
	t.committed = true
	return nil
}
func (t *docRefFakeTx) Rollback(ctx context.Context) error {
	t.rolledBack = true
	return nil
}
func (t *docRefFakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.rowIdx >= len(t.scanRows) {
		return &docRefFakeRow{err: pgx.ErrNoRows}
	}
	r := t.scanRows[t.rowIdx]
	t.rowIdx++
	return &docRefFakeRow{docRef: r.docRef, remark5: r.remark5, err: r.err, scanOneInt: r.scanOneInt}
}
func (t *docRefFakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execCalls = append(t.execCalls, docRefExecCall{sql: sql, args: args})
	if t.execErr != nil {
		return pgconn.CommandTag{}, t.execErr
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (t *docRefFakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	panic("not implemented")
}
func (t *docRefFakeTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	panic("not implemented")
}
func (t *docRefFakeTx) LargeObjects() pgx.LargeObjects { panic("not implemented") }
func (t *docRefFakeTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	panic("not implemented")
}
func (t *docRefFakeTx) Conn() *pgx.Conn { panic("not implemented") }
func (t *docRefFakeTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	panic("not implemented")
}

type docRefFakeRow struct {
	docRef     string
	remark5    string
	err        error
	scanOneInt *int
}

func (r *docRefFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.scanOneInt != nil {
		if ptr, ok := dest[0].(*int); ok {
			*ptr = *r.scanOneInt
		}
		return nil
	}
	if len(dest) >= 2 {
		if ptr, ok := dest[0].(*string); ok {
			*ptr = r.docRef
		}
		if ptr, ok := dest[1].(*string); ok {
			*ptr = r.remark5
		}
	}
	return nil
}

// fakeTxBeginner wraps a docRefFakeTx and returns it from Begin.
type fakeTxBeginner struct {
	tx *docRefFakeTx
}

func (b *fakeTxBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	return b.tx, nil
}

// --- helpers ---

func mustParseAppError(err error) *appError {
	var ae *appError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// --- tests ---

func TestUpdatePurchaseOrderDocRefHappyPath(t *testing.T) {
	tx := &docRefFakeTx{
		scanRows: []docRefScanRow{
			{docRef: "2604.82", remark5: "1107071348495692"},
		},
	}
	pool := &fakeTxBeginner{tx: tx}

	result, err := updatePurchaseOrderDocRef(context.Background(), pool, "POL26060022", updateDocRefPayload{
		DocRef:            "7417.69",
		ExpectedOldDocRef: "2604.82",
		ExpectedRemark5:   "1107071348495692",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed=true")
	}
	if result.OldDocRef != "2604.82" {
		t.Errorf("OldDocRef = %q, want 2604.82", result.OldDocRef)
	}
	if result.NewDocRef != "7417.69" {
		t.Errorf("NewDocRef = %q, want 7417.69", result.NewDocRef)
	}
	if len(tx.execCalls) != 2 {
		t.Errorf("expected 2 UPDATE calls (header + detail), got %d", len(tx.execCalls))
	}
	if !tx.committed {
		t.Error("transaction was not committed")
	}
	// Verify the new doc_ref value was used in updates.
	for _, call := range tx.execCalls {
		if len(call.args) > 0 {
			if s, ok := call.args[0].(string); ok && s != "7417.69" {
				t.Errorf("UPDATE arg doc_ref = %q, want 7417.69", s)
			}
		}
	}
}

func TestUpdatePurchaseOrderDocRefDryRun(t *testing.T) {
	tx := &docRefFakeTx{
		scanRows: []docRefScanRow{
			{docRef: "2604.82", remark5: "ORD001"},
		},
	}
	pool := &fakeTxBeginner{tx: tx}

	result, err := updatePurchaseOrderDocRef(context.Background(), pool, "POL26060022", updateDocRefPayload{
		DocRef: "7417.69",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.DryRun {
		t.Error("expected DryRun=true in result")
	}
	if !result.Changed {
		t.Error("expected Changed=true (old 2604.82 ≠ new 7417.69)")
	}
	// No UPDATE calls for dry-run.
	if len(tx.execCalls) != 0 {
		t.Errorf("expected 0 UPDATE calls for dry-run, got %d", len(tx.execCalls))
	}
}

func TestUpdatePurchaseOrderDocRefExpectedOldMismatch(t *testing.T) {
	tx := &docRefFakeTx{
		scanRows: []docRefScanRow{
			{docRef: "3720.19", remark5: "ORD002"},
		},
	}
	pool := &fakeTxBeginner{tx: tx}

	_, err := updatePurchaseOrderDocRef(context.Background(), pool, "POL26060023", updateDocRefPayload{
		DocRef:            "7417.69",
		ExpectedOldDocRef: "2604.82", // wrong — actual is 3720.19
	})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	ae := mustParseAppError(err)
	if ae == nil {
		t.Fatalf("expected appError, got %T: %v", err, err)
	}
	if ae.Code != "doc_ref_changed" {
		t.Errorf("error code = %q, want doc_ref_changed", ae.Code)
	}
	if ae.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", ae.Status)
	}
}

func TestUpdatePurchaseOrderDocRefRemark5Mismatch(t *testing.T) {
	tx := &docRefFakeTx{
		scanRows: []docRefScanRow{
			{docRef: "2604.82", remark5: "ORD001"},
		},
	}
	pool := &fakeTxBeginner{tx: tx}

	_, err := updatePurchaseOrderDocRef(context.Background(), pool, "POL26060022", updateDocRefPayload{
		DocRef:          "7417.69",
		ExpectedRemark5: "WRONG_ORDER", // wrong
	})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	ae := mustParseAppError(err)
	if ae == nil {
		t.Fatalf("expected appError, got %T: %v", err, err)
	}
	if ae.Code != "remark_5_mismatch" {
		t.Errorf("error code = %q, want remark_5_mismatch", ae.Code)
	}
	if ae.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", ae.Status)
	}
}

func TestUpdatePurchaseOrderDocRefNotFound(t *testing.T) {
	tx := &docRefFakeTx{
		scanRows: []docRefScanRow{
			{err: pgx.ErrNoRows}, // first query: main lookup returns not found
			{err: pgx.ErrNoRows}, // second query: other-trans_flag lookup also not found
		},
	}
	pool := &fakeTxBeginner{tx: tx}

	_, err := updatePurchaseOrderDocRef(context.Background(), pool, "POL99999999", updateDocRefPayload{
		DocRef: "7417.69",
	})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	ae := mustParseAppError(err)
	if ae == nil {
		t.Fatalf("expected appError, got %T: %v", err, err)
	}
	if ae.Code != "purchase_order_not_found" {
		t.Errorf("error code = %q, want purchase_order_not_found", ae.Code)
	}
	if ae.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", ae.Status)
	}
}

func TestUpdateDocRefPayloadJSONBinding(t *testing.T) {
	raw := `{
		"doc_ref": "7417.69",
		"expected_old_doc_ref": "2604.82",
		"expected_remark_5": "1107071348495692",
		"dry_run": true
	}`
	var p updateDocRefPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.DocRef != "7417.69" {
		t.Errorf("DocRef = %q", p.DocRef)
	}
	if p.ExpectedOldDocRef != "2604.82" {
		t.Errorf("ExpectedOldDocRef = %q", p.ExpectedOldDocRef)
	}
	if p.ExpectedRemark5 != "1107071348495692" {
		t.Errorf("ExpectedRemark5 = %q", p.ExpectedRemark5)
	}
	if !p.DryRun {
		t.Error("DryRun should be true")
	}
}

func TestUpdateDocRefResultJSONFields(t *testing.T) {
	res := updateDocRefResult{
		DocNo:             "POL26060022",
		OldDocRef:         "2604.82",
		NewDocRef:         "7417.69",
		OldRemark5:        "1107071348495692",
		UpdatedDetailRows: 3,
		Changed:           true,
		DryRun:            false,
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"doc_no", "old_doc_ref", "new_doc_ref", "old_remark_5", "updated_detail_rows", "changed", "dry_run"} {
		if _, ok := m[key]; !ok {
			t.Errorf("expected JSON key %q", key)
		}
	}
}

// Verify TransFlagPurchaseOrder constant is used consistently.
func TestUpdateDocRefUsesCorrectTransFlag(t *testing.T) {
	if models.TransFlagPurchaseOrder != 6 {
		t.Errorf("TransFlagPurchaseOrder = %d, want 6", models.TransFlagPurchaseOrder)
	}
}
