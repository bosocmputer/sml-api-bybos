package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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
			Capability        string   `json:"capability"`
			Versions          []string `json:"versions"`
			MaxBytes          int64    `json:"max_request_bytes"`
			MaxItems          int      `json:"max_items"`
			MaxText           int      `json:"max_text_characters"`
			CorrelationHeader string   `json:"correlation_header"`
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
	if response.Data.CorrelationHeader != "X-Correlation-ID" {
		t.Fatalf("unexpected correlation header: %+v", response.Data)
	}
}

func TestGatewayCapabilitiesAdvertiseEverySalesProfileRouteAndRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	NewWriteHandler(nil, nil).Capabilities(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			ContractRevision string `json:"contract_revision"`
			DocumentProfile  struct {
				Versions         []string `json:"versions"`
				Routes           []string `json:"routes"`
				MaxRequestBytes  int64    `json:"max_request_bytes"`
				MaxInputItems    int      `json:"max_input_items"`
				MaxExpandedItems int      `json:"max_expanded_items"`
			} `json:"document_profile"`
			Cancellation struct {
				FullDocumentOnly      bool `json:"full_document_only"`
				SourceLockWaitSeconds int  `json:"source_lock_wait_seconds"`
			} `json:"cancellation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ContractRevision != salesProfileContractRevision {
		t.Fatalf("contract revision=%q", response.Data.ContractRevision)
	}
	wantRoutes := []string{"creditnote", "saleinvoice", "saleinvoicecancel", "saleorder", "saleordercancel"}
	if !reflect.DeepEqual(response.Data.DocumentProfile.Routes, wantRoutes) {
		t.Fatalf("routes=%v want=%v", response.Data.DocumentProfile.Routes, wantRoutes)
	}
	if !reflect.DeepEqual(response.Data.DocumentProfile.Versions, []string{documentProfileV1}) ||
		response.Data.DocumentProfile.MaxRequestBytes != maxDocumentRequestBytes ||
		response.Data.DocumentProfile.MaxInputItems != maxDocumentItems ||
		response.Data.DocumentProfile.MaxExpandedItems != maxDocumentItems {
		t.Fatalf("document profile capability=%+v", response.Data.DocumentProfile)
	}
	if !response.Data.Cancellation.FullDocumentOnly || response.Data.Cancellation.SourceLockWaitSeconds != sourceDocumentLockWaitSeconds {
		t.Fatalf("cancellation capability=%+v", response.Data.Cancellation)
	}
}

func TestLegacyDocumentResponseContractDoesNotGainProfileFields(t *testing.T) {
	response := documentWriteResponse(docPayload{DocNo: "BF-1"}, routeSaleInvoice, false, 2, erpLogResult{Status: "created"})
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

func TestProfileResponseKeepsCoreSuccessWhenLogsDatabaseIsDown(t *testing.T) {
	payload := profilePayloadForTest()
	payload.ProfilePayloadHash = strings.Repeat("a", 64)
	response := documentWriteResponse(payload, routeSaleInvoice, false, 4, erpLogResult{
		Status: "warning", Warning: "บันทึก SML erp_logs ไม่สำเร็จ: เชื่อมต่อฐานข้อมูล logs ไม่ได้",
	})
	if response["core_status"] != "created" || response["profile_status"] != "needs_reconciliation" || response["reconciliation_required"] != true {
		t.Fatalf("response=%+v", response)
	}
	if !strings.Contains(response["log_warning"].(string), "erp_logs") {
		t.Fatalf("safe recovery cause missing: %+v", response)
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

func TestDocumentProfileRejectsOversizedBodyBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/ic/sale-invoices",
		strings.NewReader(`{"padding":"`+strings.Repeat("x", int(maxDocumentRequestBytes)+1)+`"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	NewWriteHandler(nil, nil).CreateSaleInvoice(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_json") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDocumentProfileRequiresAuthenticatedTenantBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := profilePayloadForTest()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/ic/sale-invoices", strings.NewReader(string(body)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	NewWriteHandler(nil, nil).CreateSaleInvoice(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "tenant_required") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDocumentProfileRejectsInvalidExactDecimalBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := profilePayloadForTest()
	payload.TotalValueDecimal = "1e2"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("tenant", "aoy")
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/ic/sale-invoices", strings.NewReader(string(body)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	NewWriteHandler(nil, nil).CreateSaleInvoice(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "base-10 decimal string") {
		t.Fatalf("invalid exact decimal reached the database path: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProfileMainLogEscapesHTMLAsLiteralData(t *testing.T) {
	payload := profilePayloadForTest()
	payload.Remark = `<script>alert("x")</script>`
	if err := normalizeAndValidate(&payload, payload.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	encoded := buildMainLogData1(payload, routeSaleInvoice)
	if strings.Contains(encoded, "<script>") || strings.Contains(encoded, "</script>") {
		t.Fatalf("script markup was not escaped: %s", encoded)
	}
}

func BenchmarkDocumentProfileNormalizeAndCanonicalHash(b *testing.B) {
	for _, itemCount := range []int{1, 10, 50, 200} {
		b.Run(fmt.Sprintf("items_%d", itemCount), func(b *testing.B) {
			base := profilePayloadForTest()
			base.VATRate = 0
			base.VATRateDecimal = "0"
			base.TotalValue = float64(itemCount)
			base.TotalBeforeVAT = float64(itemCount)
			base.TotalAfterVAT = float64(itemCount)
			base.TotalAmount = float64(itemCount)
			base.TotalValueDecimal = fmt.Sprintf("%d.00", itemCount)
			base.TotalBeforeVATDecimal = fmt.Sprintf("%d.00", itemCount)
			base.TotalAfterVATDecimal = fmt.Sprintf("%d.00", itemCount)
			base.TotalAmountDecimal = fmt.Sprintf("%d.00", itemCount)
			base.TotalVATValue = 0
			base.TotalVATValueDecimal = "0.00"
			base.Details = make([]docItem, itemCount)
			for i := range base.Details {
				base.Details[i] = docItem{
					ItemCode: fmt.Sprintf("ITEM-%03d", i), UnitCode: "EA", LineNumber: i,
					Qty: 1, Price: 1, PriceExcludeVAT: 1, SumAmount: 1, SumAmountExclVAT: 1,
					QtyDecimal: "1", PriceDecimal: "1", PriceExcludeVATDecimal: "1",
					DiscountAmountDecimal: "0", SumAmountDecimal: "1", VATAmountDecimal: "0",
					SumAmountExclVATDecimal: "1",
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				payload := base
				payload.Details = append([]docItem(nil), base.Details...)
				if err := normalizeAndValidate(&payload, payload.Details, routeSaleInvoice); err != nil {
					b.Fatal(err)
				}
				if _, err := canonicalProfileHash("aoy", payload, payload.Details, routeSaleInvoice); err != nil {
					b.Fatal(err)
				}
			}
		})
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
	if first != "21f38aa96983afb7ed038e3290f6b15213d241aca53ac9110d7d5270924b8897" {
		t.Fatalf("canonical Sale Invoice hash changed: got %s", first)
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

func TestSaleInvoiceProfileVATRegisterCoversEverySMLVATMode(t *testing.T) {
	tests := []struct {
		name                            string
		vatType                         int
		beforeVAT, vat, afterVAT, total string
	}{
		{name: "external", vatType: 0, beforeVAT: "300.00", vat: "21.00", afterVAT: "321.00", total: "321.00"},
		{name: "included", vatType: 1, beforeVAT: "280.37", vat: "19.63", afterVAT: "300.00", total: "300.00"},
		{name: "zero_rate", vatType: 2, beforeVAT: "0.00", vat: "0.00", afterVAT: "0.00", total: "300.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := profilePayloadForTest()
			p.VATType = tt.vatType
			p.VATRate = 7
			p.VATRateDecimal = "7.00"
			p.TotalValue, p.TotalValueDecimal = 300, "300.00"
			p.TotalBeforeVAT, _ = strconv.ParseFloat(tt.beforeVAT, 64)
			p.TotalBeforeVATDecimal = tt.beforeVAT
			p.TotalVATValue, _ = strconv.ParseFloat(tt.vat, 64)
			p.TotalVATValueDecimal = tt.vat
			p.TotalAfterVAT, _ = strconv.ParseFloat(tt.afterVAT, 64)
			p.TotalAfterVATDecimal = tt.afterVAT
			p.TotalAmount, _ = strconv.ParseFloat(tt.total, 64)
			p.TotalAmountDecimal = tt.total
			p.Details[0].Price, p.Details[0].SumAmount = 300, 300
			p.Details[0].PriceDecimal, p.Details[0].SumAmountDecimal = "300.00", "300.00"
			p.Details[0].PriceExcludeVAT, p.Details[0].SumAmountExclVAT = p.TotalBeforeVAT, p.TotalBeforeVAT
			if tt.vatType == 2 {
				p.Details[0].PriceExcludeVAT, p.Details[0].SumAmountExclVAT = 300, 300
			}
			p.Details[0].PriceExcludeVATDecimal = strconv.FormatFloat(p.Details[0].PriceExcludeVAT, 'f', 2, 64)
			p.Details[0].SumAmountExclVATDecimal = strconv.FormatFloat(p.Details[0].SumAmountExclVAT, 'f', 2, 64)
			p.Details[0].VATAmount, p.Details[0].TotalVATValue = p.TotalVATValue, p.TotalVATValue
			p.Details[0].VATAmountDecimal = tt.vat

			if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
				t.Fatal(err)
			}
			tx := &docRefFakeTx{}
			if _, err := writeProfileRelations(context.Background(), tx, p, routeSaleInvoice, strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
			if len(tx.execCalls) != 2 || !strings.Contains(tx.execCalls[0].sql, "gl_journal_vat_sale") {
				t.Fatalf("VAT mode %d must write one VAT register and one main log; calls=%d", tt.vatType, len(tx.execCalls))
			}
			expectedBeforeVAT, _, _ := normalizeExactDecimal("test", tt.beforeVAT, false)
			expectedVAT, _, _ := normalizeExactDecimal("test", tt.vat, false)
			if fmt.Sprint(tx.execCalls[0].args[3]) != expectedBeforeVAT || fmt.Sprint(tx.execCalls[0].args[5]) != expectedVAT {
				t.Fatalf("VAT register base/amount args=%#v, want %s/%s", tx.execCalls[0].args[3:6], tt.beforeVAT, tt.vat)
			}

			body, err := buildERPLogDataNew(p, routeSaleInvoice)
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(body, &data); err != nil {
				t.Fatal(err)
			}
			var vatRows []map[string]any
			if err := json.Unmarshal(data["screenvatsale"], &vatRows); err != nil {
				t.Fatal(err)
			}
			if len(vatRows) != 1 || vatRows[0]["base_caltax_amount"] != expectedBeforeVAT || vatRows[0]["amount"] != expectedVAT {
				t.Fatalf("screenvatsale=%+v", vatRows)
			}
			response := documentWriteResponse(p, routeSaleInvoice, false, 1, erpLogResult{Status: "created"})
			if !testContainsString(response["required_checks"].([]string), "vat") || !testContainsString(response["completed_checks"].([]string), "vat") {
				t.Fatalf("response VAT checks missing: %+v", response)
			}
		})
	}
}

func testContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSaleInvoiceProfileRejectsVATTotalsThatDoNotMatchMode(t *testing.T) {
	p := profilePayloadForTest()
	p.VATType = 2
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err == nil || !strings.Contains(err.Error(), "vat_type 2") {
		t.Fatalf("zero-rate non-zero header VAT totals must fail before core write: %v", err)
	}
}

func TestSaleOrderProfileUsesVerifiedRelationsAndDetailDirection(t *testing.T) {
	p := profilePayloadForTest()
	p.DocNo = "BF-SO26090001"
	p.DocFormatCode = "SO"
	p.ShipmentApplicability = "required"
	p.MarketplacePhysicalGoods = true
	p.Shipment = &docShipment{TransportName: "Synthetic", TransportAddress: "Synthetic address", TransportTelephone: "0000000000"}
	p.Items = append([]docItem(nil), p.Details...)
	p.Details = nil
	if err := normalizeAndValidate(&p, p.Items, routeSaleOrder); err != nil {
		t.Fatalf("Sale Order Profile V1 rejected: %v", err)
	}
	if got := documentDetailCalcFlag(routeSaleOrder); got != 1 {
		t.Fatalf("sale order detail calc_flag=%d want 1", got)
	}
	if got := documentDetailCalcFlag(routeSaleInvoice); got != -1 {
		t.Fatalf("sale invoice detail calc_flag=%d want -1", got)
	}

	tx := &docRefFakeTx{}
	if _, err := writeProfileRelations(context.Background(), tx, p, routeSaleOrder, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if len(tx.execCalls) != 2 || strings.Contains(tx.execCalls[0].sql, "gl_journal_vat_sale") ||
		!strings.Contains(tx.execCalls[0].sql, "ic_trans_shipment") {
		t.Fatalf("Sale Order must write shipment + main log and no VAT: %+v", tx.execCalls)
	}
	mainLog := tx.execCalls[1]
	if !testArgsContain(mainLog.args, "menu_so_sale_order") {
		t.Fatalf("Sale Order main log menu missing: args=%#v", mainLog.args)
	}

	body, err := buildERPLogDataNew(p, routeSaleOrder)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	wantSections := []string{"screenbottom", "screendetail", "screenmore", "screenshipment", "screentop"}
	if len(data) != len(wantSections) {
		t.Fatalf("Sale Order ERP sections=%v", reflect.ValueOf(data).MapKeys())
	}
	for _, section := range wantSections {
		if _, ok := data[section]; !ok {
			t.Fatalf("Sale Order ERP log missing %s", section)
		}
	}
	for _, forbidden := range []string{"screenvatsale", "screengldetail", "screengltop", "screenpay", "screenpaydeposit", "screenwithholdingtax"} {
		if _, ok := data[forbidden]; ok {
			t.Fatalf("Sale Order ERP log must not contain placeholder %s", forbidden)
		}
	}
	assertJSONKeys(t, data["screenbottom"], []string{"remark_2", "remark_3", "remark_4", "remark_5"})
	assertJSONKeys(t, data["screenmore"], []string{
		"credit_date", "credit_day", "discount_word", "expire_date", "expire_day", "remark",
		"send_date", "send_day", "send_type", "total_after_vat", "total_amount", "total_before_vat",
		"total_discount", "total_except_vat", "total_value", "total_vat_value", "vat_rate",
	})
	assertJSONKeys(t, data["screentop"], []string{
		"contactor", "cust_code", "doc_date", "doc_format_code", "doc_no", "doc_ref",
		"doc_ref_date", "doc_time", "inquiry_type", "sale_code", "sale_group", "vat_type",
	})
	encoded := buildMainLogData1(p, routeSaleOrder)
	if strings.Contains(encoded, "tax_doc_no") || strings.Contains(encoded, "tax_doc_date") {
		t.Fatalf("Sale Order main log must not claim tax-document fields: %s", encoded)
	}
	taxNo, taxDate := profileHeaderTaxDocument(routeSaleOrder, p, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	if taxNo != "" || taxDate != nil {
		t.Fatalf("Sale Order header tax fields=(%q,%v), want blank", taxNo, taxDate)
	}
}

func assertJSONKeys(t *testing.T, raw json.RawMessage, want []string) {
	t.Helper()
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	gotKeys := make([]string, 0, len(got))
	for key := range got {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	sort.Strings(want)
	if !reflect.DeepEqual(gotKeys, want) {
		t.Fatalf("JSON keys=%v want %v", gotKeys, want)
	}
}

func TestSaleOrderProfileHashAndLogsFailureRemainRecoverable(t *testing.T) {
	p := profilePayloadForTest()
	p.DocNo = "BF-SO26090002"
	p.Items = append([]docItem(nil), p.Details...)
	p.Details = nil
	if err := normalizeAndValidate(&p, p.Items, routeSaleOrder); err != nil {
		t.Fatal(err)
	}
	hash, err := canonicalProfileHash("aoy", p, p.Items, routeSaleOrder)
	if err != nil || len(hash) != 64 {
		t.Fatalf("Sale Order canonical hash=%q err=%v", hash, err)
	}
	invoiceHash, err := canonicalProfileHash("aoy", p, p.Items, routeSaleInvoice)
	if err != nil || invoiceHash == hash {
		t.Fatalf("route identity missing from hash: saleorder=%q invoice=%q err=%v", hash, invoiceHash, err)
	}
	p.ProfilePayloadHash = hash
	response := documentWriteResponse(p, routeSaleOrder, false, 3, erpLogResult{Status: "warning", Warning: "logs unavailable"})
	if response["core_status"] != "created" || response["profile_status"] != "needs_reconciliation" || response["reconciliation_required"] != true {
		t.Fatalf("Sale Order core must remain committed when logs fail: %+v", response)
	}
	checks := response["required_checks"].([]string)
	if testContainsString(checks, "vat") || !testContainsString(checks, "erp_log") {
		t.Fatalf("Sale Order checks=%v", checks)
	}
	if err := validateStoredProfileHash(hash, hash, p.DocNo); err != nil {
		t.Fatalf("identical retry rejected: %v", err)
	}
	if err := validateStoredProfileHash(hash, strings.Repeat("c", 64), p.DocNo); err == nil {
		t.Fatal("different Sale Order payload hash must conflict")
	}
}

func testArgsContain(values []any, want string) bool {
	for _, value := range values {
		if fmt.Sprint(value) == want {
			return true
		}
	}
	return false
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
	vatCall := tx.execCalls[0]
	if len(vatCall.args) != 16 {
		t.Fatalf("VAT profile args=%d, want 16 so VAT metadata and document identity use independent PostgreSQL parameters", len(vatCall.args))
	}
	if vatCall.args[1] != p.DocNo || vatCall.args[2] != p.DocNo {
		t.Fatalf("VAT doc identity args=%#v, want independent doc_no/vat_number values", vatCall.args[:3])
	}
	if fmt.Sprint(vatCall.args[10]) != "9" || fmt.Sprint(vatCall.args[11]) != "2569" {
		t.Fatalf("VAT effective period/year args=%#v, want September 2569", vatCall.args[10:12])
	}
	if fmt.Sprint(vatCall.args[13]) != "0" {
		t.Fatalf("VAT sale-register type arg=%#v, want 0 independent from the header VAT mode", vatCall.args[13])
	}
	if !strings.Contains(vatCall.sql, "vat_effective_period,vat_effective_year") ||
		!strings.Contains(vatCall.sql, "branch_code,vat_type") ||
		!strings.Contains(vatCall.sql, "$10,1,$11,$12,$13,$14") ||
		!strings.Contains(vatCall.sql, "doc_no=$15 AND trans_flag=$16") {
		t.Fatal("VAT profile must persist Buddhist effective period/year, keep the sale-register type independent, and separate NOT EXISTS parameters")
	}
	shipmentCall := tx.execCalls[1]
	if len(shipmentCall.args) != 11 || !strings.Contains(shipmentCall.sql, "doc_no=$10 AND trans_flag=$11") {
		t.Fatalf("shipment profile must separate INSERT and NOT EXISTS parameters: args=%d sql=%s", len(shipmentCall.args), shipmentCall.sql)
	}
	mainLogCall := tx.execCalls[2]
	if len(mainLogCall.args) != 12 || !strings.Contains(mainLogCall.sql, "doc_no=$10 AND screen_code=$11") ||
		!strings.Contains(mainLogCall.sql, "data2=$12") {
		t.Fatalf("main log profile must separate INSERT and NOT EXISTS parameters: args=%d sql=%s", len(mainLogCall.args), mainLogCall.sql)
	}
	if !strings.Contains(mainLogCall.args[1].(string), hash) {
		t.Fatal("main log must durably store canonical payload hash")
	}
}

func TestProfileERPLogDataNewHasFrozenSMLSections(t *testing.T) {
	p := profilePayloadForTest()
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	body, err := buildERPLogDataNew(p, routeSaleInvoice)
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
	var vatRows []struct {
		VATEffectivePeriod int `json:"vat_effective_period"`
		VATEffectiveYear   int `json:"vat_effective_year"`
		VATType            int `json:"vat_type"`
	}
	if err := json.Unmarshal(data["screenvatsale"], &vatRows); err != nil {
		t.Fatal(err)
	}
	if len(vatRows) != 1 || vatRows[0].VATEffectivePeriod != 9 || vatRows[0].VATEffectiveYear != 2569 || vatRows[0].VATType != 0 {
		t.Fatalf("screenvatsale=%+v, want effective period 9/year 2569 and sale-register vat_type 0", vatRows)
	}
	var top struct {
		VATType int `json:"vat_type"`
	}
	if err := json.Unmarshal(data["screentop"], &top); err != nil {
		t.Fatal(err)
	}
	if top.VATType != 1 {
		t.Fatalf("screentop vat_type=%d, want route-controlled header vat_type 1", top.VATType)
	}
}

func TestProfileVATEffectivePeriodRollsOverToNextBuddhistYear(t *testing.T) {
	p := profilePayloadForTest()
	p.DocDate = "2027-01-01"
	if err := normalizeAndValidate(&p, p.Details, routeSaleInvoice); err != nil {
		t.Fatal(err)
	}
	tx := &docRefFakeTx{}
	if _, err := writeProfileRelations(context.Background(), tx, p, routeSaleInvoice, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	vatCall := tx.execCalls[0]
	if fmt.Sprint(vatCall.args[10]) != "1" || fmt.Sprint(vatCall.args[11]) != "2570" {
		t.Fatalf("VAT effective period/year args=%#v, want January 2570", vatCall.args[10:12])
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
