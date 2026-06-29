package handlers

import (
	"testing"
	"time"

	"sml-api-bybos/internal/models"
)

func TestResolveDocNoRoute(t *testing.T) {
	tests := []struct {
		in        string
		wantName  string
		wantFlag  int
		wantFound bool
	}{
		{in: "saleorder", wantName: "saleorder", wantFlag: models.TransFlagSaleOrder, wantFound: true},
		{in: "SI", wantName: "saleinvoice", wantFlag: models.TransFlagSaleInvoice, wantFound: true},
		{in: "creditnote", wantName: "creditnote", wantFlag: models.TransFlagCreditNote, wantFound: true},
		{in: "CN", wantName: "creditnote", wantFlag: models.TransFlagCreditNote, wantFound: true},
		{in: "sale_cancel", wantName: "creditnote", wantFlag: models.TransFlagCreditNote, wantFound: true},
		{in: "purchase_order", wantName: "purchaseorder", wantFlag: models.TransFlagPurchaseOrder, wantFound: true},
		{in: "unknown", wantFound: false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := resolveDocNoRoute(tt.in)
			if ok != tt.wantFound {
				t.Fatalf("found = %v, want %v", ok, tt.wantFound)
			}
			if !ok {
				return
			}
			if got.name != tt.wantName || got.transFlag != tt.wantFlag {
				t.Fatalf("route = %+v, want %s/%d", got, tt.wantName, tt.wantFlag)
			}
		})
	}
}

func TestNextDocNoFromExisting(t *testing.T) {
	date := time.Date(2026, 5, 25, 9, 0, 0, 0, time.Local)
	route := docNoRoute{name: "purchaseorder", transFlag: models.TransFlagPurchaseOrder}
	got, err := nextDocNoFromExisting(route, "BF-PO", "YYMM####", date, []string{
		"BF-PO26050001",
		"BF-PO26050009",
		"BF-PO26049999",
		"BF-PO2605ABCD",
		"OTHER26050099",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LastDocNo != "BF-PO26050009" || got.LastSeq != 9 {
		t.Fatalf("last = %s/%d, want BF-PO26050009/9", got.LastDocNo, got.LastSeq)
	}
	if got.NextDocNo != "BF-PO26050010" || got.NextSeq != 10 {
		t.Fatalf("next = %s/%d, want BF-PO26050010/10", got.NextDocNo, got.NextSeq)
	}
}

func TestNextDocNoFromExistingNoRows(t *testing.T) {
	date := time.Date(2026, 5, 25, 9, 0, 0, 0, time.Local)
	route := docNoRoute{name: "saleorder", transFlag: models.TransFlagSaleOrder}
	got, err := nextDocNoFromExisting(route, "SO", "YYYYMMDD#####", date, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 0 || got.LastDocNo != "" {
		t.Fatalf("last = %s/%d, want empty/0", got.LastDocNo, got.LastSeq)
	}
	if got.NextDocNo != "SO2026052500001" || got.NextSeq != 1 {
		t.Fatalf("next = %s/%d, want SO2026052500001/1", got.NextDocNo, got.NextSeq)
	}
}

func TestDocNoRegexRejectsFormatWithoutSequence(t *testing.T) {
	_, err := nextDocNoFromExisting(docNoRoute{name: "saleorder", transFlag: models.TransFlagSaleOrder}, "SO", "YYMM", time.Now(), nil)
	if err == nil {
		t.Fatal("expected error for format without # sequence")
	}
}

func TestEscapeSQLLike(t *testing.T) {
	got := escapeSQLLike(`BF_%\PO`)
	if got != `BF\_\%\\PO` {
		t.Fatalf("escape = %q", got)
	}
}
