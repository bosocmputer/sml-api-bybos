package compat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"errors"
	"github.com/gin-gonic/gin"
)

func TestDocumentProfileCapabilityIsExplicitAndVersioned(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	NewWriteHandler(nil, nil).DocumentProfileCapabilities(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Capability string   `json:"capability"`
			Versions   []string `json:"versions"`
			MaxBytes   int64    `json:"max_request_bytes"`
			MaxItems   int      `json:"max_items"`
			MaxText    int      `json:"max_text_characters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Capability != "sml_document_profile" || len(response.Data.Versions) != 1 || response.Data.Versions[0] != documentProfileV1 {
		t.Fatalf("unexpected capability: %+v", response.Data)
	}
	if response.Data.MaxBytes != maxDocumentRequestBytes || response.Data.MaxItems != maxDocumentItems || response.Data.MaxText != maxProfileTextRunes {
		t.Fatalf("unexpected limits: %+v", response.Data)
	}
}

func TestLegacyDocumentResponseContractDoesNotGainProfileFields(t *testing.T) {
	response := documentWriteResponse(docPayload{DocNo: "BF-1"}, false, 2, erpLogResult{Status: "created"})
	for _, key := range []string{"doc_no", "status", "rows_written", "log_status"} {
		if _, ok := response[key]; !ok {
			t.Fatalf("legacy response missing %q: %+v", key, response)
		}
	}
	for _, key := range []string{"payload_hash", "core_status", "profile_status", "required_checks", "completed_checks", "reconciliation_required"} {
		if _, ok := response[key]; ok {
			t.Fatalf("legacy response unexpectedly contains %q: %+v", key, response)
		}
	}
}

func TestNormalizeAndValidateDocumentProfileBoundaryChecks(t *testing.T) {
	valid := profilePayloadForTest()
	valid.DocumentProfileVersion = "future-version"
	if err := normalizeAndValidate(&valid, valid.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "document_profile_version") {
		t.Fatalf("unsupported version error=%v", err)
	}

	valid = profilePayloadForTest()
	valid.Remark = "safe\nunsafe"
	if err := normalizeAndValidate(&valid, valid.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "control") {
		t.Fatalf("control character error=%v", err)
	}

	valid = profilePayloadForTest()
	valid.Remark2 = strings.Repeat("ก", 256)
	if err := normalizeAndValidate(&valid, valid.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "255") {
		t.Fatalf("oversized text error=%v", err)
	}

	valid = profilePayloadForTest()
	valid.Details = make([]docItem, maxDocumentItems+1)
	if err := normalizeAndValidate(&valid, valid.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("oversized item count error=%v", err)
	}
}

func profilePayloadForTest() docPayload {
	return docPayload{
		DocumentProfileVersion:   documentProfileV1,
		MarketplacePhysicalGoods: false, ShipmentApplicability: "not_applicable",
		DocNo: "BF-INV26090001", DocDate: "2026-09-02", DocTime: "10:00",
		DocFormatCode: "SI", CustCode: "AR-1", VATType: 1, VATRate: 7, VATRateDecimal: "7.00",
		TotalValue: 100, TotalBeforeVAT: 93.46, TotalVATValue: 6.54, TotalAfterVAT: 100, TotalAmount: 100,
		TotalValueDecimal: "100.00", TotalDiscountDecimal: "0.00", TotalBeforeVATDecimal: "93.46",
		TotalVATValueDecimal: "6.54", TotalExceptVATDecimal: "0.00", TotalAfterVATDecimal: "100.00",
		TotalAmountDecimal: "100.00", Remark5: "NEXFLOW|shopee_realtime|ORDER-1",
		Details: []docItem{{
			ItemCode: "AH-1", UnitCode: "ชิ้น", LineNumber: 0, Qty: 1, Price: 100,
			PriceExcludeVAT: 93.46, SumAmount: 100, VATAmount: 6.54, TotalVATValue: 6.54,
			SumAmountExclVAT: 93.46, QtyDecimal: "1.00", PriceDecimal: "100.00",
			PriceExcludeVATDecimal: "93.46", DiscountAmountDecimal: "0.00", SumAmountDecimal: "100.00",
			VATAmountDecimal: "6.54", SumAmountExclVATDecimal: "93.46",
		}},
	}
}

func TestProfileCanonicalHashIsStableAndBusinessSensitive(t *testing.T) {
	p := profilePayloadForTest()
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	first, err := canonicalProfileHash("aoy", p, p.Details, routeSaleInvoice)
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalProfileHash("aoy", p, p.Details, routeSaleInvoice)
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("hashes=%q/%q err=%v", first, second, err)
	}
	p.Remark = "changed"
	changed, _ := canonicalProfileHash("aoy", p, p.Details, routeSaleInvoice)
	if changed == first {
		t.Fatal("business field change must change payload hash")
	}
	otherTenant, _ := canonicalProfileHash("demo", p, p.Details, routeSaleInvoice)
	if otherTenant == changed {
		t.Fatal("authenticated tenant must be part of idempotency hash")
	}
}

func TestProfileValidationFailsBeforeCoreForIncompleteShipmentAndDecimalMismatch(t *testing.T) {
	p := profilePayloadForTest()
	p.MarketplacePhysicalGoods = true
	p.ShipmentApplicability = "required"
	p.Shipment = &docShipment{TransportName: "Shopee API", TransportAddress: "", TransportTelephone: "0999999999"}
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "transport_address") {
		t.Fatalf("incomplete shipment error=%v", err)
	}

	p = profilePayloadForTest()
	p.TotalAmountDecimal = "99.00"
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "numeric compatibility") {
		t.Fatalf("decimal mismatch error=%v", err)
	}
}

func TestProfileRelationsWriteVATShipmentAndMainLogInCallerTransaction(t *testing.T) {
	p := profilePayloadForTest()
	p.MarketplacePhysicalGoods = true
	p.ShipmentApplicability = "required"
	p.Shipment = &docShipment{TransportName: "Shopee API", TransportAddress: "Synthetic address", TransportTelephone: "0000000000"}
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	hash, err := canonicalProfileHash("aoy", p, p.Details, routeSaleInvoice)
	if err != nil {
		t.Fatal(err)
	}
	tx := &docRefFakeTx{}
	rows, err := writeProfileRelations(context.Background(), tx, p, routeSaleInvoice, hash)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || len(tx.execCalls) != 3 {
		t.Fatalf("rows/calls=%d/%d", rows, len(tx.execCalls))
	}
	joined := tx.execCalls[0].sql + tx.execCalls[1].sql + tx.execCalls[2].sql
	for _, relation := range []string{"gl_journal_vat_sale", "ic_trans_shipment", "INSERT INTO logs"} {
		if !strings.Contains(joined, relation) {
			t.Fatalf("transactional profile SQL missing %s", relation)
		}
	}
	if !strings.Contains(tx.execCalls[2].args[1].(string), hash) {
		t.Fatal("main log must durably store canonical payload hash")
	}
}

func TestProfileERPLogDataNewHasFrozenSMLSections(t *testing.T) {
	p := profilePayloadForTest()
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	body, err := buildERPLogDataNew(p)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{
		"screenbottom", "screendetail", "screengldetail", "screengltop", "screenmore",
		"screenpay", "screenpaydeposit", "screenshipment", "screentop", "screenvatsale", "screenwithholdingtax",
	} {
		if _, ok := data[section]; !ok {
			t.Fatalf("data_new missing section %s", section)
		}
	}
}

func TestStoredProfileHashMismatchIsConflict(t *testing.T) {
	err := validateStoredProfileHash("old-hash", "new-hash", "BF-1")
	var appErr *appError
	if !errors.As(err, &appErr) || appErr.Status != http.StatusConflict || appErr.Code != "doc_no_payload_mismatch" {
		t.Fatalf("error=%#v", err)
	}
	if err := validateStoredProfileHash("same", "same", "BF-1"); err != nil {
		t.Fatal(err)
	}
}

func TestProfileERPLogFillsExistingMinimalRow(t *testing.T) {
	p := profilePayloadForTest()
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	pool := &fakeERPLogPool{exists: false, rowsAffected: 1}
	status, err := insertERPLog(context.Background(), pool, p, routeSaleInvoice)
	if err != nil {
		t.Fatal(err)
	}
	if status != "updated" || !strings.Contains(pool.execSQL, "UPDATE erp_logs") || !strings.Contains(pool.execSQL, "data_new") {
		t.Fatalf("status/sql=%q/%s", status, pool.execSQL)
	}
}
