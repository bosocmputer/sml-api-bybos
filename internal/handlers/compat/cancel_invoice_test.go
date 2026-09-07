package compat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestCancellationProfileNeverAdoptsManualSMLDocument(t *testing.T) {
	err := validateExistingCancellationProfileOwnership("", strings.Repeat("a", 64), "INV-1", "CN-1")
	var appErr *appError
	if !errors.As(err, &appErr) || appErr.Code != "source_already_cancelled_externally" || appErr.Status != 409 {
		t.Fatalf("manual ownership error=%#v", err)
	}
	if err := validateExistingCancellationProfileOwnership(strings.Repeat("a", 64), strings.Repeat("a", 64), "INV-1", "CN-1"); err != nil {
		t.Fatalf("matching owned profile rejected: %v", err)
	}
	err = conflictingCancellationError("INV-1", "SIC-1", models.TransFlagSaleInvoiceCancel)
	if !errors.As(err, &appErr) || appErr.Code != "source_already_cancelled" {
		t.Fatalf("cross-kind error=%#v", err)
	}
}

func TestCancellationDateValidationPreservesLegacyCompatibility(t *testing.T) {
	legacy := saleInvoiceCancelRequest{DocNo: "CN-LEGACY", DocDate: "legacy-invalid-date"}
	if err := normalizeCancellationProfileRequest(&legacy, true); err != nil {
		t.Fatalf("legacy request must retain date fallback behavior: %v", err)
	}
	profile := saleInvoiceCancelRequest{
		DocumentProfileVersion: documentProfileV1,
		DocNo:                  "CN-PROFILE",
		DocDate:                "invalid-profile-date",
		Remark5:                "NEXFLOW|shopee_realtime|ORDER-1",
	}
	if err := normalizeCancellationProfileRequest(&profile, true); err == nil || !strings.Contains(err.Error(), "doc_date format") {
		t.Fatalf("Profile V1 must reject an ambiguous document date: %v", err)
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

func TestSaleInvoiceVoidProfileIsHeaderOnlyAndMatchesFrozenAuditShape(t *testing.T) {
	src := saleInvoiceForCancelFixture(1)
	req := profileCancellationRequest("BF-SIC26090001", "SIC")
	p, err := saleInvoiceCancellationProfilePayload("aoy", src, req, routeSaleInvoiceCancel)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Details) != 0 || p.TotalAmountDecimal != "0" || p.DocRef != src.DocNo {
		t.Fatalf("SIC profile=%+v", p)
	}
	tx := &docRefFakeTx{}
	if _, err := writeProfileRelations(context.Background(), tx, p, routeSaleInvoiceCancel, p.ProfilePayloadHash); err != nil {
		t.Fatal(err)
	}
	if len(tx.execCalls) != 1 || !strings.Contains(tx.execCalls[0].sql, "INSERT INTO logs") || testCallsContainSQL(tx.execCalls, "gl_journal") || testCallsContainSQL(tx.execCalls, "ap_ar") {
		t.Fatalf("SIC relation writes=%+v", tx.execCalls)
	}
	if !testArgsContain(tx.execCalls[0].args, "menu_so_invoice_cancel") {
		t.Fatalf("SIC main-log menu missing: %#v", tx.execCalls[0].args)
	}
	body, err := buildERPLogDataNew(p, routeSaleInvoiceCancel)
	if err != nil {
		t.Fatal(err)
	}
	assertERPSections(t, body, []string{"screenbottom", "screendetail", "screenmore", "screentop"})
}

func TestCreditNoteProfileMatchesVATReceivableAndExactSourceReferences(t *testing.T) {
	for _, vatType := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("vat_type_%d", vatType), func(t *testing.T) {
			src := saleInvoiceForCancelFixture(vatType)
			req := profileCancellationRequest(fmt.Sprintf("BF-CN-%d", vatType), "CN")
			p, err := saleInvoiceCancellationProfilePayload("aoy", src, req, routeCreditNote)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Details) != 1 || p.Details[0].RefDocNo != src.DocNo || p.Details[0].BranchCode != "" || p.Details[0].DocRefType != 1 {
				t.Fatalf("CN references=%+v", p.Details)
			}
			refAmount, receivableAmount := creditNoteReferenceAmounts(src)
			if refAmount != src.TotalValueDecimal || receivableAmount != src.TotalAmountDecimal {
				t.Fatalf("reference authority=%s/%s source=%+v", refAmount, receivableAmount, src)
			}
			tx := &docRefFakeTx{}
			if _, err := writeProfileRelations(context.Background(), tx, p, routeCreditNote, p.ProfilePayloadHash); err != nil {
				t.Fatal(err)
			}
			if len(tx.execCalls) != 3 || !strings.Contains(tx.execCalls[0].sql, "gl_journal_vat_sale") ||
				!strings.Contains(tx.execCalls[1].sql, "ap_ar_trans_detail") || !strings.Contains(tx.execCalls[2].sql, "INSERT INTO logs") {
				t.Fatalf("CN relation writes=%+v", tx.execCalls)
			}
			body, err := buildERPLogDataNew(p, routeCreditNote)
			if err != nil {
				t.Fatal(err)
			}
			assertERPSections(t, body, []string{"screenbottom", "screendetail", "screengldetail", "screengltop", "screenmore", "screenpay", "screentop", "screenvatsale", "screenwithholdingtax"})
			var sections map[string]json.RawMessage
			_ = json.Unmarshal(body, &sections)
			var details []map[string]any
			_ = json.Unmarshal(sections["screendetail"], &details)
			if len(details) != 1 || details[0]["ref_doc_no"] != src.DocNo || details[0]["branch_code"] != "" {
				t.Fatalf("CN ERP detail=%+v", details)
			}
		})
	}
}

func profileCancellationRequest(docNo, format string) saleInvoiceCancelRequest {
	return saleInvoiceCancelRequest{
		DocumentProfileVersion: documentProfileV1, DocNo: docNo, DocDate: "2026-09-03", DocTime: "11:25",
		DocFormatCode: format, Remark: "Synthetic cancellation", Remark2: "full reversal",
		Remark5: "NEXFLOW|shopee_realtime|ORDER-1", CreatorCode: "BILLFLOW", CashierCode: "BILLFLOW", UserRequest: "NEXFLOW",
	}
}

func saleInvoiceForCancelFixture(vatType int) saleInvoiceForCancel {
	before, vat, after, total := "280.37", "19.63", "300", "300"
	if vatType == 0 {
		before, vat, after, total = "300", "21", "321", "321"
	}
	if vatType == 2 {
		before, vat, after, total = "0", "0", "0", "300"
	}
	detailBefore := before
	if vatType == 2 {
		detailBefore = "300"
	}
	return saleInvoiceForCancel{
		DocNo: "INV-SYNTHETIC", DocDate: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), DocTime: "11:24",
		DocFormatCode: "INV", CustCode: "AR-1", BranchCode: "", WHFrom: "AB-1", LocationFrom: "001",
		VATType: vatType, VATRate: 7, VATRateDecimal: "7", TotalValue: 300, TotalValueDecimal: "300",
		TotalBeforeVATDecimal: before, TotalVATValueDecimal: vat, TotalAfterVATDecimal: after,
		TotalAmountDecimal: total, TotalDiscountDecimal: "0", TotalExceptVATDecimal: "0",
		Items: []saleInvoiceCancelLine{{LineNumber: 0, ItemCode: "AH-0001", ItemName: "Synthetic item", UnitCode: "ชิ้น",
			WHCode: "AB-1", ShelfCode: "001", BranchCode: "", Qty: 1, QtyDecimal: "1", Price: 300,
			PriceDecimal: "300", PriceExcludeVATDecimal: detailBefore, DiscountAmountDecimal: "0",
			TotalVATValueDecimal: vat, SumAmountDecimal: "300", SumAmountExclVATDecimal: detailBefore,
			TaxType: 1, VATType: vatType}},
	}
}

func assertERPSections(t *testing.T, body []byte, want []string) {
	t.Helper()
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(body, &sections); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(sections))
	for key := range sections {
		got = append(got, key)
	}
	if !sameSortedStrings(got, want) {
		t.Fatalf("ERP sections=%v want=%v", got, want)
	}
}

func testCallsContainSQL(calls []docRefExecCall, fragment string) bool {
	for _, call := range calls {
		if strings.Contains(call.sql, fragment) {
			return true
		}
	}
	return false
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
