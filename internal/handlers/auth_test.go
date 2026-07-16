package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeAuthQuerier struct {
	databaseRows []any
	userRows     [][]any
	lastSQL      string
	lastArgs     []any
}

func (q *fakeAuthQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.lastSQL = sql
	q.lastArgs = args
	return fakeAuthRow{values: q.databaseRows}
}

func (q *fakeAuthQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.lastSQL = sql
	q.lastArgs = args
	return &fakeAuthRows{rows: q.userRows, idx: -1}, nil
}

type fakeAuthRow struct {
	values []any
	err    error
}

func (r fakeAuthRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(r.values) == 0 {
		return pgx.ErrNoRows
	}
	for i := range dest {
		switch target := dest[i].(type) {
		case *string:
			*target = r.values[i].(string)
		default:
			return errors.New("unsupported scan target")
		}
	}
	return nil
}

type fakeAuthRows struct {
	rows [][]any
	idx  int
	err  error
}

func (r *fakeAuthRows) Close() {}
func (r *fakeAuthRows) Err() error {
	return r.err
}
func (r *fakeAuthRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}
func (r *fakeAuthRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *fakeAuthRows) Next() bool {
	if r.idx+1 >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}
func (r *fakeAuthRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.rows) {
		return errors.New("scan before next")
	}
	row := r.rows[r.idx]
	for i := range dest {
		switch target := dest[i].(type) {
		case *string:
			*target = row[i].(string)
		case *int16:
			*target = row[i].(int16)
		default:
			return errors.New("unsupported scan target")
		}
	}
	return nil
}
func (r *fakeAuthRows) Values() ([]any, error) {
	if r.idx < 0 || r.idx >= len(r.rows) {
		return nil, errors.New("values before next")
	}
	return r.rows[r.idx], nil
}
func (r *fakeAuthRows) RawValues() [][]byte {
	return nil
}
func (r *fakeAuthRows) Conn() *pgx.Conn {
	return nil
}

func TestLookupDatabaseNormalizesTenant(t *testing.T) {
	q := &fakeAuthQuerier{
		databaseRows: []any{"SML", "STPT", "STPT", "STPT"},
	}

	got, err := (&AuthHandler{}).lookupDatabase(context.Background(), q, "sml", "stpt")
	if err != nil {
		t.Fatal(err)
	}
	if got.DataGroup != "SML" || got.DataCode != "STPT" || got.Tenant != "stpt" {
		t.Fatalf("database = %+v", got)
	}
	if !strings.Contains(q.lastSQL, "sml_database_list") {
		t.Fatalf("unexpected SQL: %s", q.lastSQL)
	}
}

func TestListSyncCandidateUsersFiltersInactiveExceptSuperadminAndHashesPlainPassword(t *testing.T) {
	q := &fakeAuthQuerier{
		userRows: [][]any{
			{"001", "sml", "plain123", int16(2), int16(1)},
			{"PUI", "pui", "5f4dcc3b5aa765d61d8327deb882cf99", int16(3), int16(1)},
			{"superadmin", "System Administrator", "adminpass", int16(2), int16(0)},
			{"OLD", "inactive", "oldpass", int16(0), int16(0)},
		},
	}
	db := smlLoginDatabase{DataGroup: "SML", DataCode: "STPT"}

	users, summary, err := (&AuthHandler{}).listSyncCandidateUsers(context.Background(), q, db)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalAllowed != 4 || summary.Active != 3 || summary.SkippedInactive != 1 || summary.PasswordNotSynced != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(users) != 3 {
		t.Fatalf("users len = %d", len(users))
	}
	if users[0].UserCode != "001" || !users[0].PasswordSynced || !strings.HasPrefix(users[0].PasswordHash, "$2") {
		t.Fatalf("plain password user = %+v", users[0])
	}
	if users[1].UserCode != "PUI" || users[1].PasswordSynced || users[1].PasswordHash != "" || users[1].PasswordIssue == "" {
		t.Fatalf("hashed password user = %+v", users[1])
	}
	if users[2].UserCode != "superadmin" || !users[2].PasswordSynced || !strings.HasPrefix(users[2].PasswordHash, "$2") {
		t.Fatalf("inactive built-in superadmin = %+v", users[2])
	}
	if !strings.Contains(q.lastSQL, "sml_user_and_group") {
		t.Fatalf("sync query should include group users: %s", q.lastSQL)
	}
}

func TestLooksLikeOneWayHash(t *testing.T) {
	cases := map[string]bool{
		"plain123":                         false,
		"5f4dcc3b5aa765d61d8327deb882cf99": true,
		"$2a$10$abcdef":                    true,
		"":                                 false,
	}
	for value, want := range cases {
		if got := looksLikeOneWayHash(value); got != want {
			t.Fatalf("looksLikeOneWayHash(%q) = %v, want %v", value, got, want)
		}
	}
}
