package handlers

import (
	"strings"
	"testing"
)

func TestNormalizeReceiptRequestRequiresExpenseOnlyWhenProvidedLater(t *testing.T) {
	req := createReceiptRequest{
		DocDate:       "2026-05-26",
		DocTime:       "15:10",
		DocFormatCode: "RC",
		PassbookCode:  "68667871",
		Lines: []createReceiptLine{{
			OrderSN:      " 260518PQXYSD3T ",
			InvoiceDocNo: " BF-INV26050048 ",
			PayoutAmount: 220,
		}},
	}
	if err := normalizeReceiptRequest(&req); err != nil {
		t.Fatal(err)
	}
	if req.Lines[0].OrderSN != "260518PQXYSD3T" || req.Lines[0].InvoiceDocNo != "BF-INV26050048" {
		t.Fatalf("line was not trimmed: %+v", req.Lines[0])
	}
}

func TestNormalizeReceiptRequestRejectsDuplicateOrder(t *testing.T) {
	req := createReceiptRequest{
		DocDate:       "2026-05-26",
		DocTime:       "15:10",
		DocFormatCode: "RC",
		PassbookCode:  "68667871",
		Lines: []createReceiptLine{
			{OrderSN: "260518PQXYSD3T", PayoutAmount: 220},
			{OrderSN: "260518PQXYSD3T", PayoutAmount: 220},
		},
	}
	err := normalizeReceiptRequest(&req)
	if err == nil || !strings.Contains(err.Error(), "duplicate order_sn") {
		t.Fatalf("error = %v, want duplicate order_sn", err)
	}
}

func TestNormalizeOrderSNsTrimsDedupesAndLimits(t *testing.T) {
	got := normalizeOrderSNs([]string{" A ", "", "B", "A", "C"}, 2)
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("got %#v", got)
	}
}
