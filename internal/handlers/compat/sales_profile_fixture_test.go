package compat

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

type salesProfileGoldenFixture struct {
	ContractRevision string `json:"contract_revision"`
	ProfileVersion   string `json:"profile_version"`
	Synthetic        bool   `json:"synthetic"`
	ContainsBuyerPII bool   `json:"contains_buyer_pii"`
	Limits           struct {
		MaxRequestBytes  int64 `json:"max_request_bytes"`
		MaxInputItems    int   `json:"max_input_items"`
		MaxExpanded      int   `json:"max_expanded_items"`
		MaxExpandedBytes int64 `json:"max_expanded_bytes"`
	} `json:"limits"`
	Routes []struct {
		Name               string   `json:"name"`
		Path               string   `json:"path"`
		TransFlag          int      `json:"trans_flag"`
		HeaderOnly         bool     `json:"header_only"`
		DetailCalcFlag     int      `json:"detail_calc_flag"`
		RequiredRelations  []string `json:"required_relations"`
		ForbiddenRelations []string `json:"forbidden_relations"`
		MainLogScreen      int      `json:"main_log_screen"`
		MainLogMenu        string   `json:"main_log_menu"`
		ERPSections        []string `json:"erp_sections"`
	} `json:"routes"`
	VATModes []struct {
		VATType          int    `json:"vat_type"`
		BeforeVAT        string `json:"total_before_vat"`
		VAT              string `json:"total_vat_value"`
		AfterVAT         string `json:"total_after_vat"`
		RegisterRequired bool   `json:"register_required"`
		RegisterVATType  int    `json:"register_vat_type"`
	} `json:"vat_modes"`
	CreditNote struct {
		DetailCalcFlag         int    `json:"detail_calc_flag"`
		DetailBranchRule       string `json:"detail_branch_rule"`
		RefAmountAuthority     string `json:"ref_amount_authority"`
		ReceivableAuthority    string `json:"receivable_authority"`
		PartialReturnSupported bool   `json:"partial_return_supported"`
	} `json:"credit_note"`
	Ownership struct {
		ManualExistingDocumentPolicy string `json:"manual_existing_document_policy"`
	} `json:"ownership"`
}

func TestSalesDocumentProfileGoldenFixtureIsCompleteAndPIIFree(t *testing.T) {
	raw, err := os.ReadFile("testdata/sml_sales_document_profile_v2_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture salesProfileGoldenFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ContractRevision == "" || fixture.ProfileVersion != documentProfileV1 || !fixture.Synthetic || fixture.ContainsBuyerPII {
		t.Fatalf("unsafe or incomplete fixture metadata: %+v", fixture)
	}
	if fixture.Limits.MaxRequestBytes != maxDocumentRequestBytes || fixture.Limits.MaxInputItems != maxDocumentItems || fixture.Limits.MaxExpanded != maxDocumentItems || fixture.Limits.MaxExpandedBytes != maxDocumentRequestBytes {
		t.Fatalf("fixture limits drifted: %+v", fixture.Limits)
	}

	wantRoutes := []string{"creditnote", "saleinvoice", "saleinvoicecancel", "saleorder", "saleordercancel"}
	gotRoutes := make([]string, 0, len(fixture.Routes))
	for _, route := range fixture.Routes {
		gotRoutes = append(gotRoutes, route.Name)
		if route.Path == "" || route.TransFlag == 0 || route.MainLogScreen != route.TransFlag {
			t.Fatalf("invalid route evidence: %+v", route)
		}
		if len(route.RequiredRelations) == 0 || len(route.ERPSections) == 0 {
			t.Fatalf("route evidence is incomplete: %+v", route)
		}
		hasGLGuard := false
		for _, relation := range route.ForbiddenRelations {
			if relation == "gl" {
				hasGLGuard = true
				break
			}
		}
		if !hasGLGuard {
			t.Fatalf("route %s does not explicitly forbid GL writes", route.Name)
		}
	}
	sort.Strings(gotRoutes)
	if !reflect.DeepEqual(gotRoutes, wantRoutes) {
		t.Fatalf("routes=%v want=%v", gotRoutes, wantRoutes)
	}

	if len(fixture.VATModes) != 3 {
		t.Fatalf("VAT modes=%d want=3", len(fixture.VATModes))
	}
	for _, mode := range fixture.VATModes {
		if !mode.RegisterRequired || mode.RegisterVATType != smlSaleVATRegisterType {
			t.Fatalf("VAT register contract drifted: %+v", mode)
		}
		if mode.VATType == 2 && (mode.BeforeVAT != "0.00" || mode.VAT != "0.00" || mode.AfterVAT != "0.00") {
			t.Fatalf("zero-rate header totals drifted: %+v", mode)
		}
	}
	if fixture.CreditNote.DetailCalcFlag != 1 || fixture.CreditNote.DetailBranchRule != "copy_source_exactly" ||
		fixture.CreditNote.RefAmountAuthority != "source.total_value" || fixture.CreditNote.ReceivableAuthority != "source.total_amount" ||
		fixture.CreditNote.PartialReturnSupported {
		t.Fatalf("credit-note contract drifted: %+v", fixture.CreditNote)
	}
	if fixture.Ownership.ManualExistingDocumentPolicy != "conflict_without_adoption" {
		t.Fatalf("manual ownership policy drifted: %+v", fixture.Ownership)
	}
}
