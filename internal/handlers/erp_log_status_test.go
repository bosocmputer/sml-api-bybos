package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeERPLogRows replays canned erp_logs rows through the pgx.Rows interface
// so the status logic can be tested without a live SML database — there is no
// DB test harness in this repo, and these branches decide whether a document
// gets blocked, so they need coverage that does not depend on a shop being
// reachable.
type fakeERPLogRows struct {
	rows []erpLogRow
	idx  int
	err  error
}

func (f *fakeERPLogRows) Close()                                       {}
func (f *fakeERPLogRows) Err() error                                   { return f.err }
func (f *fakeERPLogRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *fakeERPLogRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeERPLogRows) Values() ([]any, error)                       { return nil, nil }
func (f *fakeERPLogRows) RawValues() [][]byte                          { return nil }
func (f *fakeERPLogRows) Conn() *pgx.Conn                              { return nil }

func (f *fakeERPLogRows) Next() bool {
	if f.idx >= len(f.rows) {
		return false
	}
	f.idx++
	return true
}

func (f *fakeERPLogRows) Scan(dest ...any) error {
	r := f.rows[f.idx-1]
	*(dest[0].(*int64)) = r.roworder
	*(dest[1].(*int)) = r.functionCode
	*(dest[2].(*string)) = r.userCode
	*(dest[3].(*string)) = r.computerName
	*(dest[4].(*string)) = r.menuName
	*(dest[5].(**time.Time)) = r.dateTime
	*(dest[6].(*float64)) = r.docAmount
	*(dest[7].(*float64)) = r.oldDocAmount
	*(dest[8].(*[]byte)) = r.dataOld
	*(dest[9].(*[]byte)) = r.dataNew
	return nil
}

type fakeERPLogPool struct {
	rows     []erpLogRow
	queryErr error
}

func (f *fakeERPLogPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &fakeERPLogRows{rows: f.rows}, nil
}

func logTime(t *testing.T, value string) *time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil {
		t.Fatalf("parse test time: %v", err)
	}
	return &parsed
}

// The everyday case: PaperLess starts a job, nobody touches the document.
func TestLoadERPLogStatusNoActivityAfterBaseline(t *testing.T) {
	pool := &fakeERPLogPool{}
	status, err := loadERPLogStatus(context.Background(), pool, "2PUV2609-00085", 12, 100)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.HasMeaningfulChange || status.EditedAfterBaseline || status.CreatedAfterBaseline {
		t.Fatalf("untouched document must report no change, got %+v", status)
	}
	if status.CurrentRoworder != 100 {
		t.Fatalf("CurrentRoworder = %d, want the baseline echoed back when nothing is newer", status.CurrentRoworder)
	}
}

// A re-save with no user-visible change must not block. This is the exact
// complaint that motivated the switch away from the content hash.
func TestLoadERPLogStatusIgnoresNoOpResave(t *testing.T) {
	identical := []byte(`{"screendetail":[{"line_number":"0","item_code":"AAA","qty":"1.00000000000000","price":"10.00"}]}`)
	resaved := []byte(`{"screendetail":[{"line_number":"0","item_code":"AAA","qty":"1","price":"10"}]}`)

	pool := &fakeERPLogPool{rows: []erpLogRow{{
		roworder: 101, functionCode: erpLogFunctionEdit,
		userCode: "10392", computerName: "วอร์ม-จัดซื้อ", menuName: "ใบสั่งซื้อ",
		dateTime: logTime(t, "2026-09-15 11:44:56"),
		dataOld:  identical, dataNew: resaved,
	}}}

	status, err := loadERPLogStatus(context.Background(), pool, "2POV2609-00082", 12, 100)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.EditedAfterBaseline {
		t.Fatal("an edit row exists, so EditedAfterBaseline must be true")
	}
	if status.HasMeaningfulChange {
		t.Fatalf("a formatting-only re-save must not count as a meaningful change: %+v", status.Changes)
	}
	if status.CurrentRoworder != 101 {
		t.Fatalf("CurrentRoworder = %d, want 101", status.CurrentRoworder)
	}
}

func TestLoadERPLogStatusReportsRealEditWithAttribution(t *testing.T) {
	pool := &fakeERPLogPool{rows: []erpLogRow{{
		roworder: 102, functionCode: erpLogFunctionEdit,
		userCode: "10392", computerName: "วอร์ม-จัดซื้อ", menuName: "ใบสั่งซื้อ",
		dateTime: logTime(t, "2026-09-03 14:28:10"),
		dataOld:  []byte(`{"screenbottom":{"remark":"เดิม"}}`),
		dataNew:  []byte(`{"screenbottom":{"remark":"ใหม่"}}`),
	}}}

	status, err := loadERPLogStatus(context.Background(), pool, "2POV2609-00014", 12, 100)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.HasMeaningfulChange || status.ChangeCount != 1 {
		t.Fatalf("real edit must be reported, got %+v", status)
	}
	// Attribution is the whole point of using erp_logs: the user must be
	// told who changed what, not just that something changed.
	if status.LatestEditor != "10392" || status.LatestComputer != "วอร์ม-จัดซื้อ" {
		t.Fatalf("missing attribution: %+v", status)
	}
	if status.LatestEditedAt != "2026-09-03 14:28:10" {
		t.Fatalf("LatestEditedAt = %q, want the SML wall-clock time", status.LatestEditedAt)
	}
}

// 33 documents in six weeks were recreated under an existing doc_no with a
// different amount and no edit row. Checking only for function_code=2 would
// let that through.
func TestLoadERPLogStatusTreatsRecreateAsBlocking(t *testing.T) {
	pool := &fakeERPLogPool{rows: []erpLogRow{{
		roworder: 200, functionCode: erpLogFunctionCreate,
		userCode: "01020", computerName: "เครื่องขาย", menuName: "menu_so_invoice",
		dateTime: logTime(t, "2026-09-08 10:02:35"),
		docAmount: 12469, oldDocAmount: 0,
		dataNew: []byte(`{"screentop":{"doc_no":"2COD2609-00023"}}`),
	}}}

	status, err := loadERPLogStatus(context.Background(), pool, "2COD2609-00023", 44, 150)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.CreatedAfterBaseline || !status.HasMeaningfulChange {
		t.Fatalf("a document recreated under the same number must block: %+v", status)
	}
}

func TestLoadERPLogStatusTreatsDeleteAsBlocking(t *testing.T) {
	pool := &fakeERPLogPool{rows: []erpLogRow{{
		roworder: 300, functionCode: erpLogFunctionDelete,
		userCode: "10352", menuName: "ยกเลิกใบสั่งซื้อ",
		dateTime: logTime(t, "2026-09-02 09:52:21"),
	}}}

	status, err := loadERPLogStatus(context.Background(), pool, "2POV2609-00001", 12, 100)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.DeletedAfterBaseline || !status.HasMeaningfulChange {
		t.Fatalf("a delete row must block: %+v", status)
	}
}

// An undecodable payload must fail safe. Reporting "no change" here would
// silently let a genuinely edited document through.
func TestLoadERPLogStatusFailsSafeOnUndecodablePayload(t *testing.T) {
	pool := &fakeERPLogPool{rows: []erpLogRow{{
		roworder: 400, functionCode: erpLogFunctionEdit,
		userCode: "10392",
		dateTime: logTime(t, "2026-09-15 09:00:00"),
		dataOld:  []byte(`{"screentop":`), // truncated JSON
		dataNew:  []byte(`{}`),
	}}}

	status, err := loadERPLogStatus(context.Background(), pool, "2PUV2609-00099", 12, 100)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.DiffUnavailable || !status.HasMeaningfulChange {
		t.Fatalf("an unreadable payload must be treated as a change, got %+v", status)
	}
}

// Several edits after the baseline must accumulate, and the reported editor
// must be the most recent one.
func TestLoadERPLogStatusAggregatesMultipleEdits(t *testing.T) {
	pool := &fakeERPLogPool{rows: []erpLogRow{
		{
			roworder: 101, functionCode: erpLogFunctionEdit, userCode: "AAA",
			dateTime: logTime(t, "2026-09-15 09:00:00"),
			dataOld:  []byte(`{"screenbottom":{"remark":"1"}}`),
			dataNew:  []byte(`{"screenbottom":{"remark":"2"}}`),
		},
		{
			roworder: 102, functionCode: erpLogFunctionEdit, userCode: "BBB",
			dateTime: logTime(t, "2026-09-15 10:00:00"),
			dataOld:  []byte(`{"screenbottom":{"remark":"2"}}`),
			dataNew:  []byte(`{"screenbottom":{"remark":"3"}}`),
		},
	}}

	status, err := loadERPLogStatus(context.Background(), pool, "2PUV2609-00085", 12, 100)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.ChangeCount != 2 {
		t.Fatalf("ChangeCount = %d, want 2", status.ChangeCount)
	}
	if status.LatestEditor != "BBB" {
		t.Fatalf("LatestEditor = %q, want the most recent editor", status.LatestEditor)
	}
	if status.CurrentRoworder != 102 {
		t.Fatalf("CurrentRoworder = %d, want the newest roworder", status.CurrentRoworder)
	}
}

// A query failure must propagate, never be reported as "verified, no change".
func TestLoadERPLogStatusPropagatesQueryError(t *testing.T) {
	pool := &fakeERPLogPool{queryErr: errors.New("connection refused")}
	if _, err := loadERPLogStatus(context.Background(), pool, "2PUV2609-00085", 12, 0); err == nil {
		t.Fatal("query failure must propagate so the caller can fail safe")
	}
}

func TestLogsDBName(t *testing.T) {
	if got := logsDBName("drh"); got != "drh_logs" {
		t.Fatalf("logsDBName = %q, want drh_logs", got)
	}
}

// A tenant whose SML database was restored from a backup has no _logs sibling
// and never will. That must be reported as a distinct, permanent condition so
// the caller falls back instead of blocking every document in that shop
// forever on a question that cannot be answered there.
func TestLogsDatabaseAbsentRecognizesMissingDatabase(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"postgres missing database", errors.New(`failed to connect: database "stpt_logs" does not exist (SQLSTATE 3D000)`), true},
		{"uppercase message", errors.New(`Database "stpt_logs" DOES NOT EXIST`), true},
		{"connection refused is transient", errors.New("dial tcp 10.0.0.1:5432: connect: connection refused"), false},
		{"timeout is transient", errors.New("context deadline exceeded"), false},
		{"nil is not absent", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logsDatabaseAbsent(tc.err); got != tc.want {
				t.Fatalf("logsDatabaseAbsent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
