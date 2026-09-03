package compat

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/models"
)

type cancellationLockFake struct {
	calls []string
	err   error
}

func (f *cancellationLockFake) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.calls = append(f.calls, sql)
	return pgconn.CommandTag{}, f.err
}

func TestCreditNoteRouteUsesCreditNoteTransFlag(t *testing.T) {
	if routeCreditNote.name != "creditnote" {
		t.Fatalf("route name = %q", routeCreditNote.name)
	}
	if routeCreditNote.transFlag != models.TransFlagCreditNote {
		t.Fatalf("trans_flag = %d, want %d", routeCreditNote.transFlag, models.TransFlagCreditNote)
	}
	if routeCreditNote.transType != models.TransTypeSale || routeCreditNote.itemKey != "details" {
		t.Fatalf("route = %+v, want sale detail route", routeCreditNote)
	}
}

func TestSaleInvoiceCancelRouteUsesCancelSaleTransFlag(t *testing.T) {
	if routeSaleInvoiceCancel.name != "saleinvoicecancel" {
		t.Fatalf("route name = %q", routeSaleInvoiceCancel.name)
	}
	if routeSaleInvoiceCancel.transFlag != models.TransFlagSaleInvoiceCancel {
		t.Fatalf("trans_flag = %d, want %d", routeSaleInvoiceCancel.transFlag, models.TransFlagSaleInvoiceCancel)
	}
	if routeSaleInvoiceCancel.transType != models.TransTypeSale || routeSaleInvoiceCancel.itemKey != "" {
		t.Fatalf("route = %+v, want header-only sale cancellation route", routeSaleInvoiceCancel)
	}
}

func TestSaleOrderCancelRouteUsesVerifiedSSCContract(t *testing.T) {
	if routeSaleOrderCancel.name != "saleordercancel" || routeSaleOrderCancel.transFlag != models.TransFlagSaleOrderCancel {
		t.Fatalf("route=%+v", routeSaleOrderCancel)
	}
	if routeSaleOrderCancel.transType != models.TransTypeSale || routeSaleOrderCancel.itemKey != "details" || routeSaleOrderCancel.menuName != "" {
		t.Fatalf("route=%+v, want detail route with blank SML menu", routeSaleOrderCancel)
	}
}

func TestCancellationSourceLockIsSharedAndBounded(t *testing.T) {
	fake := &cancellationLockFake{}
	if err := acquireCancellationSourceLock(context.Background(), fake, models.TransFlagSaleInvoice, "INV-1"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 || !strings.Contains(fake.calls[0], "lock_timeout") || !strings.Contains(fake.calls[1], "pg_advisory_xact_lock") {
		t.Fatalf("calls=%v", fake.calls)
	}
	busy := &cancellationLockFake{err: &pgconn.PgError{Code: "55P03"}}
	err := acquireCancellationSourceLock(context.Background(), busy, models.TransFlagSaleInvoice, "INV-1")
	var appErr *appError
	if !errors.As(err, &appErr) || appErr.Code != "document_busy" || appErr.Status != 409 {
		t.Fatalf("err=%#v", err)
	}
}

func TestSaleOrderCancelProfileMatchesFrozenParity(t *testing.T) {
	src := saleOrderForCancelFixture()
	req := saleInvoiceCancelRequest{
		DocNo: "BF-SSC26090001", DocDate: "2026-09-03", DocTime: "13:49", DocFormatCode: "SSC",
		DocumentProfileVersion: documentProfileV1, Remark: "Shopee cancelled", Remark2: "full reversal",
		Remark5: "NEXFLOW|shopee_realtime|ORDER-1", CreatorCode: "BILLFLOW", CashierCode: "BILLFLOW", UserRequest: "NEXFLOW",
	}
	p, err := saleOrderCancelProfilePayload("aoy", src, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.DocRef != src.DocNo || len(p.Details) != 1 || p.Details[0].RefDocNo != src.DocNo || p.Details[0].RefLineNumber != 0 {
		t.Fatalf("profile payload=%+v detail=%+v", p, p.Details)
	}
	if p.ProfilePayloadHash == "" || p.TotalAmountDecimal != "600" || p.Details[0].QtyDecimal != "2" {
		t.Fatalf("exact/hash fields missing: %+v", p)
	}
	if got := cancellationDetailCalcFlag(routeSaleOrderCancel); got != -1 {
		t.Fatalf("SSC calc_flag=%d want -1", got)
	}
	body, err := buildERPLogDataNew(p, routeSaleOrderCancel)
	if err != nil {
		t.Fatal(err)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(body, &sections); err != nil {
		t.Fatal(err)
	}
	gotKeys := make([]string, 0, len(sections))
	for key := range sections {
		gotKeys = append(gotKeys, key)
	}
	wantKeys := []string{"screenbottom", "screendetail", "screenmore", "screentop"}
	if !sameSortedStrings(gotKeys, wantKeys) {
		t.Fatalf("sections=%v want=%v", gotKeys, wantKeys)
	}
	encoded := buildMainLogData1(p, routeSaleOrderCancel)
	for _, want := range []string{"cancel_type", "SO26090001", "NEXFLOW"} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("main log missing %q: %s", want, encoded)
		}
	}
}

func sameSortedStrings(got, want []string) bool {
	sortStrings := func(values []string) []string {
		out := append([]string(nil), values...)
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				if out[j] < out[i] {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}
	return reflect.DeepEqual(sortStrings(got), sortStrings(want))
}

func saleOrderForCancelFixture() saleOrderForCancel {
	return saleOrderForCancel{
		DocNo: "SO26090001", DocDate: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), DocTime: "13:48",
		DocFormatCode: "SO", CustCode: "AR-1", BranchCode: "", WHFrom: "AB-1", LocationFrom: "001",
		VATType: 1, VATRate: "7.00", InquiryType: 0, TotalValue: "600.00", TotalBeforeVAT: "560.75",
		TotalVATValue: "39.25", TotalAfterVAT: "600.00", TotalAmount: "600.00", TotalDiscount: "0",
		TotalExceptVAT: "0", Items: []saleOrderCancelLine{{LineNumber: 0, ItemCode: "AH-0001", ItemName: "Synthetic item",
			UnitCode: "กล่อง", WHCode: "AB-1", ShelfCode: "001", BranchCode: "000", Qty: "2.00", Price: "300.00",
			PriceExcludeVAT: "280.375", DiscountAmount: "0", TotalVATValue: "39.25", SumAmount: "600.00",
			SumAmountExcludeVAT: "560.75", TaxType: 1, VATType: 1}},
	}
}

func TestNormalizedCancelDocFieldsDefaultsToCreditNote(t *testing.T) {
	docDate, docTime, docFormat := normalizedCancelDocFields(saleInvoiceCancelRequest{})
	if docDate.Format("2006-01-02") != time.Now().Format("2006-01-02") {
		t.Fatalf("doc date = %s, want today", docDate.Format("2006-01-02"))
	}
	if docTime == "" {
		t.Fatal("doc time should default to current HH:mm")
	}
	if docFormat != "CN" {
		t.Fatalf("doc format = %q, want CN", docFormat)
	}
}

func TestNormalizedCancelDocFieldsKeepsProvidedValues(t *testing.T) {
	docDate, docTime, docFormat := normalizedCancelDocFields(saleInvoiceCancelRequest{
		DocDate:       "2026-06-16",
		DocTime:       "14:30",
		DocFormatCode: "CNX",
	})
	if docDate.Format("2006-01-02") != "2026-06-16" {
		t.Fatalf("doc date = %s", docDate.Format("2006-01-02"))
	}
	if docTime != "14:30" || docFormat != "CNX" {
		t.Fatalf("doc time/format = %q/%q", docTime, docFormat)
	}
}

func TestNormalizedVoidDocFieldsDefaultsToSIC(t *testing.T) {
	_, docTime, docFormat := normalizedVoidDocFields(saleInvoiceCancelRequest{})
	if docTime == "" {
		t.Fatal("doc time should default to current HH:mm")
	}
	if docFormat != "SIC" {
		t.Fatalf("doc format = %q, want SIC", docFormat)
	}
}
