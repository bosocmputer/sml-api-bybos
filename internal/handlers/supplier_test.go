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

type fakeSupplierDB struct {
	tx       *fakeSupplierTx
	beginHit int
}

func (db *fakeSupplierDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return fakeMasterRow{}
}

func (db *fakeSupplierDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &fakeMasterRows{}, nil
}

func (db *fakeSupplierDB) Begin(ctx context.Context) (supplierTx, error) {
	db.beginHit++
	return db.tx, nil
}

type fakeSupplierExec struct {
	sql  string
	args []any
}

type fakeSupplierTx struct {
	execErrs   []error
	execs      []fakeSupplierExec
	committed  bool
	rolledBack bool
}

func (tx *fakeSupplierTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, fakeSupplierExec{sql: sql, args: args})
	if len(tx.execErrs) >= len(tx.execs) && tx.execErrs[len(tx.execs)-1] != nil {
		return pgconn.CommandTag{}, tx.execErrs[len(tx.execs)-1]
	}
	return pgconn.CommandTag{}, nil
}

func (tx *fakeSupplierTx) Commit(ctx context.Context) error {
	tx.committed = true
	return nil
}

func (tx *fakeSupplierTx) Rollback(ctx context.Context) error {
	tx.rolledBack = true
	return nil
}

func TestNormalizeSupplierCreateLegacyPayloadDefaultsToCompany(t *testing.T) {
	got, err := normalizeSupplierCreateRequest(createSupplierRequest{
		Code:  "VNEW01",
		Name1: "บริษัท ใหม่ จำกัด",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.APStatus != 1 || got.BranchType != 0 || got.BranchCode != "00000" {
		t.Fatalf("defaults = ap_status=%d branch_type=%d branch_code=%q", got.APStatus, got.BranchType, got.BranchCode)
	}
	if got.Name1 != "บริษัท ใหม่ จำกัด" {
		t.Fatalf("name_1 = %q", got.Name1)
	}
}

func TestNormalizeSupplierCreateNaturalPerson(t *testing.T) {
	apStatus := 0
	branchType := 1
	got, err := normalizeSupplierCreateRequest(createSupplierRequest{
		Code:       "V-PERSON",
		APStatus:   &apStatus,
		Firstname:  "สมชาย",
		Lastname:   "ใจดี",
		Name1:      "should clear",
		NameEng1:   "should clear",
		BranchType: &branchType,
		BranchCode: "00002",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.APStatus != 0 || got.Firstname != "สมชาย" || got.Lastname != "ใจดี" {
		t.Fatalf("natural person = %+v", got)
	}
	if got.Name1 != "" || got.NameEng1 != "" {
		t.Fatalf("company fields should be cleared: %+v", got)
	}
}

func TestNormalizeSupplierCreateRejectsBranchMissingCode(t *testing.T) {
	apStatus := 1
	branchType := 1
	_, err := normalizeSupplierCreateRequest(createSupplierRequest{
		Code:       "VBRANCH",
		APStatus:   &apStatus,
		Name1:      "บริษัท มีสาขา",
		BranchType: &branchType,
	})
	if err == nil || !strings.Contains(err.Error(), "รหัสสาขา") {
		t.Fatalf("err = %v, want branch code validation", err)
	}
}

func TestNormalizeSupplierCreateRejectsDirtyCode(t *testing.T) {
	apStatus := 1
	branchType := 0
	for _, code := range []string{" V001", "V001\u200B", "V001\u0E3A"} {
		_, err := normalizeSupplierCreateRequest(createSupplierRequest{
			Code:       code,
			APStatus:   &apStatus,
			Name1:      "บริษัท",
			BranchType: &branchType,
		})
		if err == nil {
			t.Fatalf("code %q should be rejected", code)
		}
	}
}

func TestSupplierCreateCompanyWritesSupplierAndDetail(t *testing.T) {
	tx := &fakeSupplierTx{}
	w := performSupplierCreate(tx, `{
		"code":"VNEW01",
		"ap_status":1,
		"name_1":"บริษัท ใหม่ จำกัด",
		"name_eng_1":"New Co",
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
	if tx.execs[0].args[0] != "VNEW01" || tx.execs[0].args[1] != 1 || tx.execs[0].args[5] != "บริษัท ใหม่ จำกัด" {
		t.Fatalf("supplier args = %#v", tx.execs[0].args)
	}
	if tx.execs[1].args[0] != "VNEW01" || tx.execs[1].args[2] != 0 || tx.execs[1].args[3] != "00000" {
		t.Fatalf("detail args = %#v", tx.execs[1].args)
	}

	var got struct {
		Success bool            `json:"success"`
		Data    models.Supplier `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Data.Name != "บริษัท ใหม่ จำกัด" || got.Data.BranchCode != "00000" {
		t.Fatalf("response = %+v", got)
	}
}

func TestSupplierCreateDetailFailureRollsBack(t *testing.T) {
	tx := &fakeSupplierTx{execErrs: []error{nil, errors.New("detail insert failed")}}
	w := performSupplierCreate(tx, `{
		"code":"VNEW02",
		"ap_status":1,
		"name_1":"บริษัท ใหม่ จำกัด",
		"branch_type":0
	}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("tx state committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}

func TestSupplierCreateDuplicateCodeConflict(t *testing.T) {
	tx := &fakeSupplierTx{execErrs: []error{&pgconn.PgError{Code: "23505", Message: "duplicate key"}}}
	w := performSupplierCreate(tx, `{
		"code":"VNEW03",
		"ap_status":1,
		"name_1":"บริษัท ใหม่ จำกัด",
		"branch_type":0
	}`)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got api.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Code != "duplicate_supplier_code" {
		t.Fatalf("error = %+v", got.Error)
	}
}

func performSupplierCreate(tx *fakeSupplierTx, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := &SupplierHandler{
		getPool: func(ctx context.Context, tenant string) (supplierQuerier, error) {
			return &fakeSupplierDB{tx: tx}, nil
		},
	}
	r := gin.New()
	r.POST("/suppliers", func(c *gin.Context) {
		c.Set(middleware.TenantKey, "sml_test")
		h.Create(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/suppliers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}
