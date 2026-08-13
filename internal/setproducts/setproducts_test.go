package setproducts

import "testing"

func TestBuildDefinitionUsesSMLSumAmountWeights(t *testing.T) {
	definition := BuildDefinition("AH-0057", []Component{
		{LineNumber: 0, RowOrder: 1, ItemCode: "AH-0001", ItemName: "A", ItemType: 0, UnitCode: "กล่อง", Qty: 1, Price: 200, SumAmount: 200, PriceRatio: 0.2, UnitFactor: 1, Active: true, UnitValid: true},
		{LineNumber: 1, RowOrder: 2, ItemCode: "AH-0002", ItemName: "B", ItemType: 0, UnitCode: "กล่อง", Qty: 2, Price: 400, SumAmount: 800, PriceRatio: 0.4, UnitFactor: 1, Active: true, UnitValid: true},
	})
	if !definition.DocumentValid || !definition.StockValid {
		t.Fatalf("definition = %+v, want valid", definition)
	}
	if definition.WeightMethod != "sum_amount" {
		t.Fatalf("weight method = %q, want sum_amount", definition.WeightMethod)
	}
	got, err := AllocateCents(60000, definition.Components)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 12000 || got[1] != 48000 {
		t.Fatalf("allocation = %v, want [12000 48000]", got)
	}
}

func TestAllocateCentsKeepsResidualOnLastComponent(t *testing.T) {
	components := []Component{
		{Qty: 1, SumAmount: 1},
		{Qty: 1, SumAmount: 1},
		{Qty: 1, SumAmount: 1},
	}
	got, err := AllocateCents(100, components)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 33 || got[1] != 33 || got[2] != 34 {
		t.Fatalf("allocation = %v, want [33 33 34]", got)
	}
}

func TestAllocateCentsUsesExactDecimalWeights(t *testing.T) {
	components := []Component{
		{Qty: 1, SumAmount: 0.1},
		{Qty: 1, SumAmount: 0.2},
	}
	got, err := AllocateCents(101, components)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 34 || got[1] != 67 {
		t.Fatalf("allocation = %v, want exact satang [34 67]", got)
	}
}

func TestBuildDefinitionBlocksNestedAndInvalidUnits(t *testing.T) {
	definition := BuildDefinition("SET-1", []Component{
		{LineNumber: 0, ItemCode: "SET-2", ItemType: 3, UnitCode: "ชุด", Qty: 1, SumAmount: 100, UnitFactor: 1, Active: true, UnitValid: true},
		{LineNumber: 1, ItemCode: "ITEM-1", ItemType: 0, UnitCode: "ชิ้น", Qty: 1, SumAmount: 100, UnitFactor: 0, Active: true, UnitValid: false},
	})
	if definition.DocumentValid || definition.StockValid {
		t.Fatalf("definition = %+v, want blocked", definition)
	}
	if !HasWarning(definition.WarningCodes, "nested_set_not_supported") || !HasWarning(definition.WarningCodes, "set_component_unit_invalid") {
		t.Fatalf("warnings = %v", definition.WarningCodes)
	}
}

func TestBuildDefinitionAllowsDocumentButBlocksStockForServiceComponent(t *testing.T) {
	definition := BuildDefinition("SET-SERVICE", []Component{{
		ItemCode: "SERVICE", ItemType: 1, UnitCode: "ครั้ง", Qty: 1,
		SumAmount: 100, UnitFactor: 1, UnitValid: true, Active: true,
	}})
	if !definition.DocumentValid {
		t.Fatal("service component should remain valid for document expansion")
	}
	if definition.StockValid {
		t.Fatal("service component must block stock calculation")
	}
	if !HasWarning(definition.WarningCodes, "set_component_not_stock_item") {
		t.Fatalf("warning_codes = %v", definition.WarningCodes)
	}
}

func TestBuildDefinitionHashIsStableAcrossInputOrder(t *testing.T) {
	a := []Component{
		{LineNumber: 1, RowOrder: 2, ItemCode: "B", UnitCode: "ชิ้น", Qty: 2, SumAmount: 200, UnitFactor: 1, Active: true, UnitValid: true},
		{LineNumber: 0, RowOrder: 1, ItemCode: "A", UnitCode: "ชิ้น", Qty: 1, SumAmount: 100, UnitFactor: 1, Active: true, UnitValid: true},
	}
	b := []Component{a[1], a[0]}
	first := BuildDefinition("SET", a)
	second := BuildDefinition("SET", b)
	if first.Hash == "" || first.Hash != second.Hash {
		t.Fatalf("hashes = %q / %q", first.Hash, second.Hash)
	}
}
