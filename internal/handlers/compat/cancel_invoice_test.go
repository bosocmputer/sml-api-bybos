package compat

import (
	"testing"
	"time"

	"sml-api-bybos/internal/models"
)

func TestCreditNoteRouteUsesSaleCancelTransFlag(t *testing.T) {
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
