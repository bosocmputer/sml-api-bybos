package docs

import (
	"encoding/json"
	"testing"
)

func TestOpenAPISpecIsValidAndBillFlowNative(t *testing.T) {
	b, err := FS.ReadFile("openapi.json")
	if err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatalf("openapi.json must be valid JSON: %v", err)
	}

	paths := spec["paths"].(map[string]any)
	for _, path := range []string{
		"/api/v1/ar/customers",
		"/api/v1/ar/receipt-candidates",
		"/api/v1/ar/receipts",
		"/api/v1/ap/suppliers",
		"/api/v1/erp/branches",
		"/api/v1/erp/users",
		"/api/v1/erp/expenses",
		"/api/v1/erp/incomes",
		"/api/v1/erp/passbooks",
		"/api/v1/ic/doc-formats",
		"/api/v1/ic/doc-formats/by-code",
		"/api/v1/ic/doc-no/next",
		"/api/v1/ic/document-candidates/batch",
		"/api/v1/ic/units",
		"/api/v1/ic/products",
		"/api/v1/ic/products/{code}/images",
		"/api/v1/ic/products/{code}/images/{roworder}",
		"/api/v1/ic/products/{code}/units",
		"/api/v1/ic/warehouses",
		"/api/v1/marketplace/nextstep/orders",
		"/api/v1/ic/sale-orders",
		"/api/v1/ic/sale-invoices",
		"/api/v1/ic/purchase-orders",
		"/openapi.json",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("required path missing from OpenAPI spec: %s", path)
		}
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	errorResponse := schemas["ErrorResponse"].(map[string]any)
	errorProps := errorResponse["properties"].(map[string]any)["error"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"code", "message", "details"} {
		if _, ok := errorProps[field]; !ok {
			t.Fatalf("ErrorResponse.error.%s missing", field)
		}
	}

	pagedProps := schemas["PagedResponse"].(map[string]any)["properties"].(map[string]any)
	if _, ok := pagedProps["meta"]; !ok {
		t.Fatal("PagedResponse.meta missing")
	}
	if _, ok := pagedProps["page"]; ok {
		t.Fatal("PagedResponse must use meta.page, not top-level page")
	}

	for _, schema := range []string{"DocFormat", "NextDocNo", "NextStepMarketplaceOrdersResponse", "ERPMasterItem", "ReceiptCandidate", "ARReceiptRequest", "ARReceiptCreated"} {
		if _, ok := schemas[schema]; !ok {
			t.Fatalf("required schema missing from OpenAPI spec: %s", schema)
		}
	}

	docCreated := schemas["DocCreatedResponse"].(map[string]any)
	docData := docCreated["properties"].(map[string]any)["data"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"log_status", "log_warning"} {
		if _, ok := docData[field]; !ok {
			t.Fatalf("DocCreatedResponse.data.%s missing", field)
		}
	}
}

func TestSwaggerUIAssetsAreEmbedded(t *testing.T) {
	for _, name := range []string{"swagger-ui.css", "swagger-ui-bundle.js"} {
		b, err := FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(b) < 1024 {
			t.Fatalf("%s looks too small to be official Swagger UI asset", name)
		}
	}
}
