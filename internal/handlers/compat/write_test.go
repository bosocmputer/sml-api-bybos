package compat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/models"
)

func TestNormalizeAndValidateSaleInvoiceUsesDetails(t *testing.T) {
	p := &docPayload{
		DocNo:         " BF-APIQA26050001 ",
		DocDate:       "2026-05-18",
		DocFormatCode: "SI",
		CustCode:      "AR00001",
		DocTime:       "10:30",
		VATType:       0,
		VATRate:       7,
		Details: []docItem{{
			ItemCode: "BF00002",
			UnitCode: "ชิ้น",
			Qty:      1,
			Price:    100,
		}},
	}

	if err := normalizeAndValidate(p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	if p.DocNo != "BF-APIQA26050001" {
		t.Fatalf("doc_no = %q", p.DocNo)
	}
	if p.DocTime != "10:30" {
		t.Fatalf("doc_time = %q, want preserved 10:30", p.DocTime)
	}
	if p.BranchCode != "" {
		t.Fatalf("branch = %q, want no hidden default", p.BranchCode)
	}
}

func TestNormalizeAndValidateRejectsMissingDocTime(t *testing.T) {
	p := &docPayload{
		DocNo:         "BF-APIQA26050001",
		DocDate:       "2026-05-18",
		DocFormatCode: "SI",
		CustCode:      "AR00001",
		VATType:       0,
		VATRate:       7,
		Details: []docItem{{
			ItemCode: "BF00002",
			UnitCode: "ชิ้น",
			Qty:      1,
			Price:    100,
		}},
	}
	err := normalizeAndValidate(p, p.Details, routeSaleInvoice)
	if err == nil || !strings.Contains(err.Error(), "doc_time") {
		t.Fatalf("error = %v, want doc_time required", err)
	}
}

func TestNormalizeAndValidateRejectsMissingRequiredItemFields(t *testing.T) {
	p := &docPayload{
		DocNo:         "BF-APIQA26050002",
		DocDate:       "2026-05-18",
		DocTime:       "09:00",
		DocFormatCode: "PO",
		CustCode:      "V-001",
		VATType:       0,
		VATRate:       7,
		Items: []docItem{{
			ItemCode: "BF00002",
			Qty:      1,
			Price:    100,
		}},
	}

	err := normalizeAndValidate(p, p.Items, routePurchaseOrder)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "unit_code") {
		t.Fatalf("error = %v, want unit_code", err)
	}
}

func TestPurchaseOrderPayloadAcceptsBillFlowHeaderFields(t *testing.T) {
	var p docPayload
	err := json.Unmarshal([]byte(`{
		"doc_no":"BF-PO26050001",
		"doc_date":"2026-05-25",
		"doc_format_code":"PO",
		"cust_code":"V001",
		"remark":"pun_coffee",
		"remark_2":"tax",
		"remark_5":"2605236MY1Q8EH",
		"doc_ref":"1440",
		"inquiry_type":1,
		"total_discount":0,
		"items":[{"item_code":"I001","unit_code":"ชิ้น","qty":1,"price":100,"discount_amount":12.5,"sum_amount":87.5}]
	}`), &p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Remark != "pun_coffee" || p.Remark2 != "tax" || p.Remark5 != "2605236MY1Q8EH" || p.DocRef != "1440" || p.InquiryType != 1 {
		t.Fatalf("payload header fields = remark:%q remark_2:%q remark_5:%q doc_ref:%q inquiry_type:%d",
			p.Remark, p.Remark2, p.Remark5, p.DocRef, p.InquiryType)
	}
	if headerDiscountWord(p.TotalDiscount) != "" {
		t.Fatalf("header discount_word should be empty when total_discount is zero")
	}
	if len(p.Items) != 1 || p.Items[0].DiscountAmount != 12.5 || p.Items[0].SumAmount != 87.5 {
		t.Fatalf("line discount fields = %+v, want discount_amount=12.5 sum_amount=87.5", p.Items)
	}
}

func TestHeaderDiscountWordEmptyWhenTotalDiscountZero(t *testing.T) {
	if got := headerDiscountWord(0); got != "" {
		t.Fatalf("header discount_word = %q, want empty", got)
	}
	if got := headerDiscountWord(15); got != "15" {
		t.Fatalf("header discount_word = %q, want 15", got)
	}
}

func TestDocumentRoutesUseBillFlowTransFlags(t *testing.T) {
	cases := []struct {
		name      string
		route     docRoute
		transFlag int
		transType int
		itemKey   string
	}{
		{"saleorder", routeSaleOrder, models.TransFlagSaleOrder, models.TransTypeSale, "items"},
		{"saleinvoice", routeSaleInvoice, models.TransFlagSaleInvoice, models.TransTypeSale, "details"},
		{"purchaseorder", routePurchaseOrder, models.TransFlagPurchaseOrder, models.TransTypePurchase, "items"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.route.transFlag != tc.transFlag || tc.route.transType != tc.transType || tc.route.itemKey != tc.itemKey {
				t.Fatalf("route = %+v", tc.route)
			}
			if !strings.Contains(tc.route.menuName, "BillFlow") {
				t.Fatalf("menu_name = %q, want BillFlow marker", tc.route.menuName)
			}
		})
	}
}

func TestInsertERPLogWritesBillFlowAddRow(t *testing.T) {
	pool := &fakeERPLogPool{}
	p := docPayload{
		DocNo:       "BF-PO26050001",
		DocDate:     "2026-05-25",
		DocTime:     "11:15",
		CustCode:    "V001",
		TotalAmount: 123.45,
	}
	status, err := insertERPLog(context.Background(), pool, p, routePurchaseOrder)
	if err != nil {
		t.Fatal(err)
	}
	if status != "created" {
		t.Fatalf("status = %q, want created", status)
	}
	if pool.execSQL == "" {
		t.Fatal("expected erp_logs insert")
	}
	if !strings.Contains(pool.execSQL, "function_code") {
		t.Fatalf("insert SQL = %s, want function_code", pool.execSQL)
	}
	if got := pool.execArgs[5]; got != models.TransFlagPurchaseOrder {
		t.Fatalf("trans_flag arg = %v, want %d", got, models.TransFlagPurchaseOrder)
	}
	if got := pool.execArgs[6]; got != models.TransTypePurchase {
		t.Fatalf("trans_type arg = %v, want %d", got, models.TransTypePurchase)
	}
	if got := pool.execArgs[9]; got != routePurchaseOrder.menuName {
		t.Fatalf("menu_name arg = %v, want %s", got, routePurchaseOrder.menuName)
	}
}

func TestInsertERPLogSkipsDuplicateBillFlowLog(t *testing.T) {
	pool := &fakeERPLogPool{exists: true}
	p := docPayload{DocNo: "BF-SO26050001", DocDate: "2026-05-25", DocTime: "09:00", CustCode: "AR001"}
	status, err := insertERPLog(context.Background(), pool, p, routeSaleOrder)
	if err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Fatalf("status = %q, want skipped", status)
	}
	if pool.execSQL != "" {
		t.Fatalf("unexpected insert for duplicate: %s", pool.execSQL)
	}
}

func TestUpdateERPLogCreditorUpdatesBillFlowLog(t *testing.T) {
	pool := &fakeERPLogPool{rowsAffected: 1}
	status, err := updateERPLogCreditor(context.Background(), pool, "POL26060011", models.TransFlagPurchaseOrder, "AF00007")
	if err != nil {
		t.Fatal(err)
	}
	if status != "updated" {
		t.Fatalf("status = %q, want updated", status)
	}
	if !strings.Contains(pool.execSQL, "UPDATE erp_logs") {
		t.Fatalf("update SQL = %s, want erp_logs update", pool.execSQL)
	}
	if got := pool.execArgs[0]; got != "AF00007" {
		t.Fatalf("cust_code arg = %v, want AF00007", got)
	}
	if got := pool.execArgs[2]; got != models.TransFlagPurchaseOrder {
		t.Fatalf("trans_flag arg = %v, want purchase order", got)
	}
}

func TestUpdateERPLogCreditorSkipsWhenNoBillFlowLog(t *testing.T) {
	pool := &fakeERPLogPool{}
	status, err := updateERPLogCreditor(context.Background(), pool, "POL26060011", models.TransFlagPurchaseOrder, "AF00007")
	if err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Fatalf("status = %q, want skipped", status)
	}
}

func TestWarehouseValidationAllowsNullStatus(t *testing.T) {
	q := warehouseExistsSQL()
	if !strings.Contains(q, "COALESCE(status,0)=0") {
		t.Fatalf("warehouse validation must treat NULL status as active SML data: %s", q)
	}
}

type fakeERPLogPool struct {
	exists       bool
	rowErr       error
	execErr      error
	rowsAffected int64
	execSQL      string
	execArgs     []any
}

func (p *fakeERPLogPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return fakeERPLogRow{exists: p.exists, err: p.rowErr}
}

func (p *fakeERPLogPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	p.execSQL = sql
	p.execArgs = args
	if p.execErr != nil {
		return pgconn.CommandTag{}, p.execErr
	}
	if p.rowsAffected > 0 {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

type fakeERPLogRow struct {
	exists bool
	err    error
}

func (r fakeERPLogRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		if ptr, ok := dest[0].(*bool); ok {
			*ptr = r.exists
		}
	}
	return nil
}
