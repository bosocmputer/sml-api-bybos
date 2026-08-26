package compat

import (
	"testing"
	"time"

	"sml-api-bybos/internal/models"
)

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
