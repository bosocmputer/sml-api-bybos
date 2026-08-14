package compat

import (
	"errors"
	"net/http"
	"testing"

	"sml-api-bybos/internal/setproducts"
)

func TestPrepareDocumentItemsExpandsAH0058(t *testing.T) {
	definition := setproducts.BuildDefinition("AH-0058", []setproducts.Component{{
		LineNumber: 1, RowOrder: 17, ItemCode: "AH-0001", ItemName: "สีเพ้นท์",
		ItemType: 0, UnitCode: "กล่อง", Qty: 3, Price: 200, SumAmount: 600,
		PriceRatio: 0.34, UnitFactor: 1, Active: true, UnitValid: true,
	}})
	products := map[string]setproducts.Product{
		"AH-0058": {Code: "AH-0058", ItemType: 3, Active: true, Definition: &definition},
	}
	payload := docPayload{
		ExpandSetItems: true, VATType: 1, VATRate: 7, TotalValue: 600, TotalBeforeVAT: 560.75,
		TotalVATValue: 39.25, TotalAfterVAT: 600, TotalAmount: 600,
	}
	items := []docItem{{
		ItemCode: "AH-0058", UnitCode: "ชุด", Qty: 1, Price: 600,
		SumAmount: 600, SumAmountExclVAT: 560.75, TotalVATValue: 39.25,
	}}
	got, err := prepareDocumentItems(payload, items, routeSaleInvoice, products)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("prepared lines = %d, want parent + child", len(got))
	}
	parent, child := got[0], got[1]
	if parent.ItemType != 3 || parent.RefGUID == "" || parent.SetRefPrice != 600 {
		t.Fatalf("parent = %+v", parent)
	}
	if child.ItemCode != "AH-0001" || child.Qty != 3 || child.SetRefQty != 3 || child.ItemCodeMain != "AH-0058" || child.SetRefLine != parent.RefGUID {
		t.Fatalf("child = %+v", child)
	}
	if child.Price != 200 || child.SumAmount != 600 || child.PriceSetRatio != 0.34 {
		t.Fatalf("child money = %+v", child)
	}
}

func TestPrepareDocumentItemsBlocksSetWhenExpansionDisabled(t *testing.T) {
	definition := setproducts.BuildDefinition("AH-0058", []setproducts.Component{{
		LineNumber: 1, ItemCode: "AH-0001", ItemType: 0, UnitCode: "กล่อง",
		Qty: 3, SumAmount: 600, UnitFactor: 1, Active: true, UnitValid: true,
	}})
	products := map[string]setproducts.Product{
		"AH-0058": {Code: "AH-0058", ItemType: 3, Active: true, Definition: &definition},
	}
	_, err := prepareDocumentItems(docPayload{ExpandSetItems: false}, []docItem{{
		ItemCode: "AH-0058", UnitCode: "ชุด", Qty: 1, Price: 600, SumAmount: 600,
	}}, routeSaleInvoice, products)
	var appErr *appError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %v, want appError", err)
	}
	if appErr.Code != "set_product_expansion_disabled" || appErr.Status != http.StatusConflict {
		t.Fatalf("error = %+v", appErr)
	}
}

func TestPrepareDocumentItemsAllocatesResidualWithoutChangingParentTotal(t *testing.T) {
	components := []setproducts.Component{
		{LineNumber: 1, ItemCode: "A", ItemType: 0, UnitCode: "ชิ้น", Qty: 1, SumAmount: 1, UnitFactor: 1, Active: true, UnitValid: true},
		{LineNumber: 2, ItemCode: "B", ItemType: 0, UnitCode: "ชิ้น", Qty: 1, SumAmount: 1, UnitFactor: 1, Active: true, UnitValid: true},
		{LineNumber: 3, ItemCode: "C", ItemType: 0, UnitCode: "ชิ้น", Qty: 1, SumAmount: 1, UnitFactor: 1, Active: true, UnitValid: true},
	}
	definition := setproducts.BuildDefinition("SET", components)
	products := map[string]setproducts.Product{"SET": {Code: "SET", ItemType: 3, Active: true, Definition: &definition}}
	payload := docPayload{ExpandSetItems: true, TotalValue: 1, TotalBeforeVAT: 1, TotalAfterVAT: 1, TotalAmount: 1}
	got, err := prepareDocumentItems(payload, []docItem{{ItemCode: "SET", UnitCode: "ชุด", Qty: 1, Price: 1, SumAmount: 1, SumAmountExclVAT: 1}}, routeSaleOrder, products)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].SumAmount != 0.33 || got[2].SumAmount != 0.33 || got[3].SumAmount != 0.34 {
		t.Fatalf("child residual allocation = %.2f/%.2f/%.2f", got[1].SumAmount, got[2].SumAmount, got[3].SumAmount)
	}
}

func TestPrepareDocumentItemsBlocksInvalidDefinition(t *testing.T) {
	definition := setproducts.BuildDefinition("SET", nil)
	products := map[string]setproducts.Product{"SET": {Code: "SET", ItemType: 3, Active: true, Definition: &definition}}
	_, err := prepareDocumentItems(
		docPayload{ExpandSetItems: true, TotalValue: 10, TotalBeforeVAT: 10, TotalAfterVAT: 10, TotalAmount: 10},
		[]docItem{{ItemCode: "SET", UnitCode: "ชุด", Qty: 1, Price: 10, SumAmount: 10, SumAmountExclVAT: 10}},
		routeSaleInvoice, products,
	)
	if err == nil {
		t.Fatal("expected invalid set definition error")
	}
}

func TestPrepareDocumentItemsMultipliesComponentQtyPerParentSet(t *testing.T) {
	definition := setproducts.BuildDefinition("AH-0058", []setproducts.Component{{
		LineNumber: 1, RowOrder: 17, ItemCode: "AH-0001", ItemType: 0,
		UnitCode: "กล่อง", Qty: 3, Price: 200, SumAmount: 600,
		PriceRatio: 0.34, UnitFactor: 1, Active: true, UnitValid: true,
	}})
	products := map[string]setproducts.Product{
		"AH-0058": {Code: "AH-0058", ItemType: 3, Active: true, Definition: &definition},
	}
	payload := docPayload{
		ExpandSetItems: true, VATType: 1, VATRate: 7, TotalValue: 1200, TotalBeforeVAT: 1121.50,
		TotalVATValue: 78.50, TotalAfterVAT: 1200, TotalAmount: 1200,
	}
	got, err := prepareDocumentItems(payload, []docItem{{
		ItemCode: "AH-0058", UnitCode: "ชุด", Qty: 2, Price: 600,
		SumAmount: 1200, SumAmountExclVAT: 1121.50, TotalVATValue: 78.50,
	}}, routeSaleInvoice, products)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("prepared lines = %d, want parent + child", len(got))
	}
	child := got[1]
	if child.Qty != 6 || child.SetRefQty != 3 {
		t.Fatalf("child qty/set_ref_qty = %.2f/%.2f, want 6/3", child.Qty, child.SetRefQty)
	}
	if child.SumAmount != 1200 || child.Price != 200 {
		t.Fatalf("child sum/price = %.2f/%.2f, want 1200/200", child.SumAmount, child.Price)
	}
}

func TestPrepareDocumentItemsKeepsHeaderDiscountOutOfSetChildren(t *testing.T) {
	definition := setproducts.BuildDefinition("SET", []setproducts.Component{
		{LineNumber: 1, ItemCode: "A", ItemType: 0, UnitCode: "ชิ้น", Qty: 1, SumAmount: 60, UnitFactor: 1, Active: true, UnitValid: true},
		{LineNumber: 2, ItemCode: "B", ItemType: 0, UnitCode: "ชิ้น", Qty: 1, SumAmount: 40, UnitFactor: 1, Active: true, UnitValid: true},
	})
	products := map[string]setproducts.Product{"SET": {Code: "SET", ItemType: 3, Active: true, Definition: &definition}}
	payload := docPayload{ExpandSetItems: true, VATType: 1, VATRate: 7, TotalValue: 100,
		TotalDiscount: 10, TotalBeforeVAT: 84.11, TotalVATValue: 5.89, TotalAfterVAT: 90, TotalAmount: 90}
	got, err := prepareDocumentItems(payload, []docItem{{ItemCode: "SET", UnitCode: "ชุด", Qty: 1,
		Price: 100, SumAmount: 100, SumAmountExclVAT: 93.46, TotalVATValue: 6.54}}, routeSaleInvoice, products)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("prepared lines = %d, want parent + two children", len(got))
	}
	if got[0].DiscountAmount != 0 || got[1].DiscountAmount != 0 || got[2].DiscountAmount != 0 {
		t.Fatalf("line discounts must stay zero: %.2f/%.2f/%.2f", got[0].DiscountAmount, got[1].DiscountAmount, got[2].DiscountAmount)
	}
	if got[1].SumAmount+got[2].SumAmount != 100 {
		t.Fatalf("children gross = %.2f, want 100", got[1].SumAmount+got[2].SumAmount)
	}
}

func TestExistingSetParentMatchesChildrenByRefGUID(t *testing.T) {
	counts := map[string]int{"parent-guid": 2}
	if !existingSetParentHasChildren(3, "parent-guid", counts) {
		t.Fatal("expanded set parent should match children through ref_guid -> set_ref_line")
	}
	if existingSetParentHasChildren(3, "", counts) {
		t.Fatal("legacy parent without ref_guid must not be treated as an expanded set")
	}
	if !existingSetParentHasChildren(0, "", counts) {
		t.Fatal("normal product does not require set children")
	}
}
