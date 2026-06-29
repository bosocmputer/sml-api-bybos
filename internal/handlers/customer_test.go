package handlers

import (
	"bytes"
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
	"sml-api-bybos/internal/models"
)

type fakeCustomerDB struct {
	tx *fakeCustomerTx
}

func (db *fakeCustomerDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return fakeMasterRow{}
}

func (db *fakeCustomerDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &fakeMasterRows{}, nil
}

func (db *fakeCustomerDB) Begin(ctx context.Context) (customerTx, error) {
	return db.tx, nil
}

type fakeCustomerExec struct {
	sql  string
	args []any
}

type fakeCustomerTx struct {
	execErrs   []error
	execs      []fakeCustomerExec
	committed  bool
	rolledBack bool
}

func (tx *fakeCustomerTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, fakeCustomerExec{sql: sql, args: args})
	if len(tx.execErrs) >= len(tx.execs) && tx.execErrs[len(tx.execs)-1] != nil {
		return pgconn.CommandTag{}, tx.execErrs[len(tx.execs)-1]
	}
	return pgconn.CommandTag{}, nil
}

func (tx *fakeCustomerTx) Commit(ctx context.Context) error {
	tx.committed = true
	return nil
}

func (tx *fakeCustomerTx) Rollback(ctx context.Context) error {
	tx.rolledBack = true
	return nil
}

func TestNormalizeCustomerCreateLegacyPayloadDefaultsToCompany(t *testing.T) {
	got, err := normalizeCustomerCreateRequest(createCustomerRequest{
		Code:  "ARNEW01",
		Name1: "บริษัท ลูกค้าใหม่ จำกัด",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ARStatus != 1 || got.BranchType != 0 || got.BranchCode != "00000" {
		t.Fatalf("defaults = ar_status=%d branch_type=%d branch_code=%q", got.ARStatus, got.BranchType, got.BranchCode)
	}
	if got.Name1 != "บริษัท ลูกค้าใหม่ จำกัด" {
		t.Fatalf("name_1 = %q", got.Name1)
	}
}

func TestNormalizeCustomerCreateNaturalPerson(t *testing.T) {
	arStatus := 0
	branchType := 1
	got, err := normalizeCustomerCreateRequest(createCustomerRequest{
		Code:       "AR-PERSON",
		ARStatus:   &arStatus,
		FirstName:  "สมหญิง",
		LastName:   "ใจดี",
		Name1:      "should clear",
		NameEng1:   "should clear",
		BranchType: &branchType,
		BranchCode: "00002",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ARStatus != 0 || got.FirstName != "สมหญิง" || got.LastName != "ใจดี" {
		t.Fatalf("natural person = %+v", got)
	}
	if got.Name1 != "" || got.NameEng1 != "" {
		t.Fatalf("company fields should be cleared: %+v", got)
	}
}

func TestNormalizeCustomerCreateRejectsBranchMissingCode(t *testing.T) {
	arStatus := 1
	branchType := 1
	_, err := normalizeCustomerCreateRequest(createCustomerRequest{
		Code:       "ARBRANCH",
		ARStatus:   &arStatus,
		Name1:      "บริษัท มีสาขา",
		BranchType: &branchType,
	})
	if err == nil || !strings.Contains(err.Error(), "รหัสสาขา") {
		t.Fatalf("err = %v, want branch code validation", err)
	}
}

func TestNormalizeCustomerCreateRejectsDirtyCode(t *testing.T) {
	arStatus := 1
	branchType := 0
	for _, code := range []string{" AR001", "AR001\u200B", "AR001\u0E3A"} {
		_, err := normalizeCustomerCreateRequest(createCustomerRequest{
			Code:       code,
			ARStatus:   &arStatus,
			Name1:      "บริษัท",
			BranchType: &branchType,
		})
		if err == nil {
			t.Fatalf("code %q should be rejected", code)
		}
	}
}

func TestCustomerSearchWhereIncludesExpandedFields(t *testing.T) {
	where, args := customerSearchWhere("010555")
	for _, want := range []string{"c.name_eng_1", "c.first_name", "c.last_name", "d.tax_id", "d.card_id"} {
		if !strings.Contains(where, want) {
			t.Fatalf("where = %s, missing %s", where, want)
		}
	}
	if args["search"] != "%010555%" {
		t.Fatalf("search arg = %#v", args["search"])
	}
}

func TestCustomerCreateCompanyWritesCustomerAndDetail(t *testing.T) {
	tx := &fakeCustomerTx{}
	w := performCustomerCreate(tx, `{
		"code":"ARNEW01",
		"ar_status":1,
		"name_1":"บริษัท ลูกค้าใหม่ จำกัด",
		"name_eng_1":"New Customer Co",
		"address":"1 Main Road",
		"tax_id":"0105559000000",
		"branch_type":0,
		"branch_code":"ignored",
		"remark":"จาก BillFlow",
		"card_id":""
	}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !tx.committed || !tx.rolledBack || len(tx.execs) != 2 {
		t.Fatalf("tx state committed=%v rolledBack=%v execs=%d", tx.committed, tx.rolledBack, len(tx.execs))
	}
	if tx.execs[0].args[0] != "ARNEW01" || tx.execs[0].args[1] != 1 || tx.execs[0].args[5] != "บริษัท ลูกค้าใหม่ จำกัด" {
		t.Fatalf("customer args = %#v", tx.execs[0].args)
	}
	if tx.execs[1].args[0] != "ARNEW01" || tx.execs[1].args[2] != 0 || tx.execs[1].args[3] != "00000" {
		t.Fatalf("detail args = %#v", tx.execs[1].args)
	}

	var got struct {
		Success bool            `json:"success"`
		Data    models.Customer `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Data.Name != "บริษัท ลูกค้าใหม่ จำกัด" || got.Data.BranchCode != "00000" {
		t.Fatalf("response = %+v", got)
	}
}

func TestCustomerCreateDetailFailureRollsBack(t *testing.T) {
	tx := &fakeCustomerTx{execErrs: []error{nil, errors.New("detail insert failed")}}
	w := performCustomerCreate(tx, `{
		"code":"ARNEW02",
		"ar_status":1,
		"name_1":"บริษัท ลูกค้าใหม่ จำกัด",
		"branch_type":0
	}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("tx state committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}

func TestCustomerCreateDuplicateCodeConflict(t *testing.T) {
	tx := &fakeCustomerTx{execErrs: []error{&pgconn.PgError{Code: "23505", Message: "duplicate key"}}}
	w := performCustomerCreate(tx, `{
		"code":"ARNEW03",
		"ar_status":1,
		"name_1":"บริษัท ลูกค้าใหม่ จำกัด",
		"branch_type":0
	}`)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got api.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Code != "duplicate_customer_code" {
		t.Fatalf("error = %+v", got.Error)
	}
}

func performCustomerCreate(tx *fakeCustomerTx, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := &CustomerHandler{
		getPool: func(ctx context.Context, tenant string) (customerQuerier, error) {
			return &fakeCustomerDB{tx: tx}, nil
		},
	}
	r := gin.New()
	r.POST("/customers", func(c *gin.Context) {
		c.Set(middleware.TenantKey, "sml_test")
		h.Create(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/customers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}
