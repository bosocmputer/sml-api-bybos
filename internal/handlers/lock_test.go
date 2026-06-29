package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeLockRow implements pgx.Row for locateDocForLock tests. It scans a trans_flag
// (*int) and an is_lock_record (**int, nullable) — mirroring the real query.
type fakeLockRow struct {
	err       error
	transFlag int
	lock      *int // nil => SQL NULL
}

func (r fakeLockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) >= 1 {
		if p, ok := dest[0].(*int); ok {
			*p = r.transFlag
		}
	}
	if len(dest) >= 2 {
		if pp, ok := dest[1].(**int); ok {
			*pp = r.lock
		}
	}
	return nil
}

// fakeLockQuerier returns a preset row per table (keyed by which table substring
// appears in the SQL), so we can drive locateDocForLock's table-probing loop.
type fakeLockQuerier struct {
	icRow   pgx.Row // for ic_trans
	apArRow pgx.Row // for ap_ar_trans
}

func (q fakeLockQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "ap_ar_trans") {
		return q.apArRow
	}
	return q.icRow
}

func intp(v int) *int { return &v }

func TestLocateDocForLock(t *testing.T) {
	noRows := fakeLockRow{err: pgx.ErrNoRows}

	tests := []struct {
		name      string
		ic        pgx.Row
		apar      pgx.Row
		wantTable string
		wantFlag  int
		wantLock  int
		wantFound bool
		wantErr   bool
	}{
		{
			name:      "found in ic_trans, lock=0",
			ic:        fakeLockRow{transFlag: 6, lock: intp(0)},
			apar:      noRows,
			wantTable: "ic_trans", wantFlag: 6, wantLock: 0, wantFound: true,
		},
		{
			name:      "found in ic_trans, lock NULL -> 0",
			ic:        fakeLockRow{transFlag: 12, lock: nil},
			apar:      noRows,
			wantTable: "ic_trans", wantFlag: 12, wantLock: 0, wantFound: true,
		},
		{
			name:      "found in ic_trans, already locked",
			ic:        fakeLockRow{transFlag: 6, lock: intp(1)},
			apar:      noRows,
			wantTable: "ic_trans", wantFlag: 6, wantLock: 1, wantFound: true,
		},
		{
			name:      "not in ic_trans, found in ap_ar_trans (PB)",
			ic:        noRows,
			apar:      fakeLockRow{transFlag: 213, lock: intp(0)},
			wantTable: "ap_ar_trans", wantFlag: 213, wantLock: 0, wantFound: true,
		},
		{
			name:      "not found in either",
			ic:        noRows,
			apar:      noRows,
			wantFound: false,
		},
		{
			name:    "db error on ic_trans propagates",
			ic:      fakeLockRow{err: errors.New("boom")},
			apar:    noRows,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := fakeLockQuerier{icRow: tc.ic, apArRow: tc.apar}
			table, flag, lock, found, err := locateDocForLock(context.Background(), q, "DOC1")

			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != tc.wantFound {
				t.Errorf("found: want %v, got %v", tc.wantFound, found)
			}
			if !tc.wantFound {
				return
			}
			if table != tc.wantTable {
				t.Errorf("table: want %q, got %q", tc.wantTable, table)
			}
			if flag != tc.wantFlag {
				t.Errorf("flag: want %d, got %d", tc.wantFlag, flag)
			}
			if lock != tc.wantLock {
				t.Errorf("lock: want %d, got %d", tc.wantLock, lock)
			}
		})
	}
}
