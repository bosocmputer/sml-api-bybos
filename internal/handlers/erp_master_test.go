package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/middleware"
)

type fakeMasterPool struct {
	total     int
	items     []ERPMasterItem
	rowErr    error
	queryErr  error
	tenant    string
	lastSQL   string
	lastArgs  []any
	queryHits int
}

func (p *fakeMasterPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	p.lastSQL = sql
	p.lastArgs = args
	return fakeMasterRow{total: p.total, err: p.rowErr}
}

func (p *fakeMasterPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	p.queryHits++
	p.lastSQL = sql
	p.lastArgs = args
	if p.queryErr != nil {
		return nil, p.queryErr
	}
	return &fakeMasterRows{items: p.items, idx: -1}, nil
}

type fakeMasterRow struct {
	total int
	err   error
}

func (r fakeMasterRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int)) = r.total
	return nil
}

type fakeMasterRows struct {
	items []ERPMasterItem
	idx   int
	err   error
}

func (r *fakeMasterRows) Close() {}
func (r *fakeMasterRows) Err() error {
	return r.err
}
func (r *fakeMasterRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}
func (r *fakeMasterRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *fakeMasterRows) Next() bool {
	if r.idx+1 >= len(r.items) {
		return false
	}
	r.idx++
	return true
}
func (r *fakeMasterRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.items) {
		return errors.New("scan before next")
	}
	*(dest[0].(*string)) = r.items[r.idx].Code
	*(dest[1].(*string)) = r.items[r.idx].Name1
	return nil
}
func (r *fakeMasterRows) Values() ([]any, error) {
	if r.idx < 0 || r.idx >= len(r.items) {
		return nil, errors.New("values before next")
	}
	return []any{r.items[r.idx].Code, r.items[r.idx].Name1}, nil
}
func (r *fakeMasterRows) RawValues() [][]byte {
	return nil
}
func (r *fakeMasterRows) Conn() *pgx.Conn {
	return nil
}

func TestERPMasterBranchesList(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pool := &fakeMasterPool{
		total: 2,
		items: []ERPMasterItem{
			{Code: " B01 ", Name1: " สาขาหลัก "},
			{Code: "", Name1: "blank code must be skipped"},
		},
	}
	h := &ERPMasterHandler{
		getPool: func(ctx context.Context, tenant string) (erpMasterQuerier, error) {
			if tenant != "sml_test" {
				t.Fatalf("tenant = %q, want sml_test", tenant)
			}
			return pool, nil
		},
	}

	r := gin.New()
	r.GET("/branches", func(c *gin.Context) {
		c.Set(middleware.TenantKey, "sml_test")
		h.ListBranches(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/branches?search=B01&page=2&size=1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(pool.lastSQL, "FROM erp_branch_list") || !strings.Contains(pool.lastSQL, "ILIKE @search") {
		t.Fatalf("unexpected sql: %s", pool.lastSQL)
	}
	args, ok := pool.lastArgs[0].(pgx.NamedArgs)
	if !ok {
		t.Fatalf("args type = %T", pool.lastArgs[0])
	}
	if args["search"] != "%B01%" || args["size"] != 1 || args["offset"] != 1 {
		t.Fatalf("args = %#v", args)
	}

	var got struct {
		Success bool            `json:"success"`
		Data    []ERPMasterItem `json:"data"`
		Meta    api.PageMeta    `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Meta.Total != 2 || got.Meta.Page != 2 || got.Meta.Size != 1 {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if len(got.Data) != 1 || got.Data[0].Code != "B01" || got.Data[0].Name1 != "สาขาหลัก" {
		t.Fatalf("data = %+v", got.Data)
	}
}

func TestERPMasterUsersListEmptyResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &ERPMasterHandler{
		getPool: func(ctx context.Context, tenant string) (erpMasterQuerier, error) {
			return &fakeMasterPool{}, nil
		},
	}

	r := gin.New()
	r.GET("/users", func(c *gin.Context) {
		c.Set(middleware.TenantKey, "sml_test")
		h.ListUsers(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users?size=5000", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Success bool            `json:"success"`
		Data    []ERPMasterItem `json:"data"`
		Meta    api.PageMeta    `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 0 || got.Meta.Size != 20 {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestERPMasterDBError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &ERPMasterHandler{
		getPool: func(ctx context.Context, tenant string) (erpMasterQuerier, error) {
			return nil, errors.New("context deadline exceeded")
		},
	}

	r := gin.New()
	r.GET("/branches", func(c *gin.Context) {
		c.Set(middleware.TenantKey, "sml_test")
		h.ListBranches(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/branches", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got api.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Success || got.Error == nil || got.Error.Code != "db_pool_error" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
}
