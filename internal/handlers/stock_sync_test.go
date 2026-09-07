package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStockBalanceSQLUsesSafeOuterFilters(t *testing.T) {
	lower := strings.ToLower(stockBalanceRowsSQL)
	if strings.Contains(lower, "gen_code_list") {
		t.Fatal("stock query must never pass request values to gen_code_list")
	}
	if !strings.Contains(lower, "sml_ic_function_stock_balance_warehouse_location($1::date, '', '', '')") {
		t.Fatal("stock query must call the SML function with empty list filters")
	}
	if !strings.Contains(lower, "b.ic_code = any($2::text[])") {
		t.Fatal("stock query must filter item codes with a parameterized ANY expression")
	}
	if !strings.Contains(lower, "coalesce(i.item_type, 0) = 0") {
		t.Fatal("stock query must include only normal stock items")
	}
	if !strings.Contains(lower, "left join public.ic_warehouse") || !strings.Contains(lower, "left join public.ic_shelf") {
		t.Fatal("stock query must resolve warehouse and location names in the existing calculation query")
	}
}

func TestExcludedLocationBalancesDoNotMixUnits(t *testing.T) {
	states := []stockBalanceScopeState{newStockBalanceScopeState(StockBalanceScopeRequest{
		ScopeID: "shop:1", ScopeMode: "selected", ItemCodes: []string{"A", "B"},
		Locations: []StockLocationPair{{Warehouse: "W1", Location: "S1"}},
	})}
	accumulateStockBalanceRow(states, stockBalanceRow{ItemCode: "A", WarehouseCode: "W2", LocationCode: "S2", UnitCode: "ชิ้น", BalanceQty: 2})
	accumulateStockBalanceRow(states, stockBalanceRow{ItemCode: "B", WarehouseCode: "W2", LocationCode: "S2", UnitCode: "กล่อง", BalanceQty: 3})
	locations := sortedNonZeroStockLocations(states[0].excludedLocations)
	if len(locations) != 2 || locations[0].UnitCode == locations[1].UnitCode {
		t.Fatalf("excluded locations must stay separated by unit: %#v", locations)
	}
}

func TestNormalizeStockBatchRequest(t *testing.T) {
	req, items, err := normalizeStockBatchRequest(StockBalanceBatchRequest{
		AsOfDate:         "2026-07-01",
		AvailabilityMode: "net_sale_order_v1",
		Scopes: []StockBalanceScopeRequest{
			{ScopeID: " shop-1 ", ScopeMode: "SELECTED", ItemCodes: []string{"B", "A", "A"}, Locations: []StockLocationPair{{Warehouse: "01", Location: "A"}, {Warehouse: "01", Location: "A"}}},
			{ScopeID: "shop-2", ScopeMode: "all", ItemCodes: []string{"C", "A"}},
		},
	})
	if err != nil {
		t.Fatalf("normalize error = %v", err)
	}
	if got := strings.Join(items, ","); got != "A,B,C" {
		t.Fatalf("items = %q", got)
	}
	if req.Scopes[0].ScopeID != "shop-1" || len(req.Scopes[0].Locations) != 1 || strings.Join(req.Scopes[0].ItemCodes, ",") != "A,B" {
		t.Fatalf("normalized scope = %+v", req.Scopes[0])
	}
	if req.AvailabilityMode != stockAvailabilityNetSaleOrderV1 {
		t.Fatalf("availability mode = %q", req.AvailabilityMode)
	}
}

func TestNormalizeStockBatchDefaultsToPhysicalAvailability(t *testing.T) {
	req, _, err := normalizeStockBatchRequest(StockBalanceBatchRequest{
		AsOfDate: "2026-07-01",
		Scopes:   []StockBalanceScopeRequest{{ScopeID: "s", ScopeMode: "all", ItemCodes: []string{"A"}}},
	})
	if err != nil {
		t.Fatalf("normalize error = %v", err)
	}
	if req.AvailabilityMode != stockAvailabilityPhysicalV1 {
		t.Fatalf("availability mode = %q", req.AvailabilityMode)
	}
}

func TestNormalizeStockBatchRejectsUnknownAvailabilityMode(t *testing.T) {
	_, _, err := normalizeStockBatchRequest(StockBalanceBatchRequest{
		AsOfDate:         "2026-07-01",
		AvailabilityMode: "guess",
		Scopes:           []StockBalanceScopeRequest{{ScopeID: "s", ScopeMode: "all", ItemCodes: []string{"A"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "availability_mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestNetAvailabilitySubtractsOutstandingDemandExactly(t *testing.T) {
	state := newStockBalanceScopeState(StockBalanceScopeRequest{
		ScopeID: "shop:1", ScopeMode: "selected", ItemCodes: []string{"A"},
		Locations: []StockLocationPair{{Warehouse: "W1", Location: "S1"}},
	})
	accumulateStockBalanceRow([]stockBalanceScopeState{state}, stockBalanceRow{
		ItemCode: "A", WarehouseCode: "W1", LocationCode: "S1", UnitCode: "ชิ้น",
		BalanceQtyExact: "10.25", OutstandingQtyExact: "3.5",
	})
	item, err := finalizeStockBalanceItem(state.items["A"], stockAvailabilityNetSaleOrderV1)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if item.PhysicalBalanceQtyExact != "10.25" || item.OutstandingSalesOrderQtyExact != "3.5" || item.AvailableBalanceQtyExact != "6.75" {
		t.Fatalf("unexpected exact quantities: %+v", item)
	}
	if item.BalanceQty != 6.75 {
		t.Fatalf("balance = %v", item.BalanceQty)
	}
}

func TestPhysicalAvailabilityDoesNotSubtractOutstandingDemand(t *testing.T) {
	state := newStockBalanceScopeState(StockBalanceScopeRequest{
		ScopeID: "shop:1", ScopeMode: "all", ItemCodes: []string{"A"},
	})
	accumulateStockBalanceRow([]stockBalanceScopeState{state}, stockBalanceRow{
		ItemCode: "A", WarehouseCode: "W1", LocationCode: "S1",
		BalanceQtyExact: "10", OutstandingQtyExact: "3",
	})
	item, err := finalizeStockBalanceItem(state.items["A"], stockAvailabilityPhysicalV1)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if item.BalanceQty != 10 || item.AvailableBalanceQtyExact != "10" {
		t.Fatalf("physical mode changed legacy balance: %+v", item)
	}
}

func TestStockBalanceNetSQLUsesOneSnapshotAndValidatedDocumentStates(t *testing.T) {
	lower := strings.ToLower(stockBalanceNetRowsSQL)
	for _, fragment := range []string{
		"with requested_items as",
		"sml_ic_function_stock_balance_warehouse_location",
		"trans_flag = 36",
		"trans_flag = 44",
		"ref_doc_no",
		"stand_value",
		"divide_value",
		"count(distinct doc_date)",
		"sale_order_document_identity_ambiguous",
		"source_snapshot_at",
	} {
		if !strings.Contains(lower, fragment) {
			t.Fatalf("net query missing %q", fragment)
		}
	}
}

func TestNormalizeStockDemandEvidenceRequiresUniqueExactLines(t *testing.T) {
	request, err := normalizeStockDemandEvidenceRequest(StockDemandEvidenceBatchRequest{Lines: []StockDemandEvidenceRequestLine{{
		EvidenceID: "reservation-1:item-A", DocNo: "SO-2607-0010", Route: "SaleOrder",
		ItemCode: "A", WarehouseCode: "W1", LocationCode: "S1", ExpectedBaseQtyExact: "48.000",
	}}})
	if err != nil {
		t.Fatalf("normalize evidence: %v", err)
	}
	if request.Lines[0].Route != "saleorder" || request.Lines[0].TransFlag != 36 || request.Lines[0].ExpectedBaseQtyExact != "48.000" {
		t.Fatalf("normalized=%+v", request.Lines[0])
	}
	request.Lines = append(request.Lines, request.Lines[0])
	if _, err := normalizeStockDemandEvidenceRequest(request); err == nil {
		t.Fatal("duplicate evidence identity must fail")
	}
}

func TestStockDemandEvidenceSQLIsScopeAndDocumentExact(t *testing.T) {
	lower := strings.ToLower(stockDemandEvidenceSQL)
	for _, fragment := range []string{"jsonb_to_recordset", "d.doc_no=r.doc_no", "d.item_code=r.item_code", "d.wh_code", "d.shelf_code", "d.trans_flag=r.trans_flag", "stand_value", "divide_value", "transaction_timestamp"} {
		if !strings.Contains(lower, fragment) {
			t.Fatalf("evidence query missing %q", fragment)
		}
	}
}

func TestNormalizeStockBatchRejectsUnsafeOrAmbiguousScope(t *testing.T) {
	tests := []struct {
		name string
		req  StockBalanceBatchRequest
	}{
		{
			name: "selected without location",
			req:  StockBalanceBatchRequest{AsOfDate: "2026-07-01", Scopes: []StockBalanceScopeRequest{{ScopeID: "s", ScopeMode: "selected", ItemCodes: []string{"A"}}}},
		},
		{
			name: "all with location",
			req: StockBalanceBatchRequest{AsOfDate: "2026-07-01", Scopes: []StockBalanceScopeRequest{{
				ScopeID: "s", ScopeMode: "all", ItemCodes: []string{"A"}, Locations: []StockLocationPair{{Warehouse: "01", Location: "A"}},
			}}},
		},
		{
			name: "control character",
			req:  StockBalanceBatchRequest{AsOfDate: "2026-07-01", Scopes: []StockBalanceScopeRequest{{ScopeID: "s", ScopeMode: "all", ItemCodes: []string{"A\nB"}}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := normalizeStockBatchRequest(tt.req); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAccumulateStockBalanceRowExplainsExcludedWarehouseLocations(t *testing.T) {
	states := []stockBalanceScopeState{newStockBalanceScopeState(StockBalanceScopeRequest{
		ScopeID:                      "shop:1",
		ScopeMode:                    "selected",
		ItemCodes:                    []string{"A", "B"},
		Locations:                    []StockLocationPair{{Warehouse: "W1", Location: "S1"}},
		IncludeItemExcludedLocations: true,
	})}

	rows := []stockBalanceRow{
		{ItemCode: "A", WarehouseCode: "W1", WarehouseName: "คลังขาย", LocationCode: "S1", LocationName: "หน้าร้าน", BalanceQty: 10, UnitCode: "ชิ้น"},
		{ItemCode: "A", WarehouseCode: "W2", WarehouseName: "คลังสำรอง", LocationCode: "S2", LocationName: "ชั้นสอง", BalanceQty: 5, UnitCode: "ชิ้น"},
		{ItemCode: "B", WarehouseCode: "W2", WarehouseName: "คลังสำรอง", LocationCode: "S2", LocationName: "ชั้นสอง", BalanceQty: -8, UnitCode: "ชิ้น"},
		{ItemCode: "A", WarehouseCode: "W3", WarehouseName: "คลังตรวจนับ", LocationCode: "S3", LocationName: "พักสินค้า", BalanceQty: 4, UnitCode: "ชิ้น"},
		{ItemCode: "B", WarehouseCode: "W3", WarehouseName: "คลังตรวจนับ", LocationCode: "S3", LocationName: "พักสินค้า", BalanceQty: -4, UnitCode: "ชิ้น"},
	}
	for _, row := range rows {
		accumulateStockBalanceRow(states, row)
	}

	locations := sortedNonZeroStockLocations(states[0].excludedLocations)
	if len(locations) != 1 {
		t.Fatalf("excluded locations = %#v, want one non-zero location", locations)
	}
	got := locations[0]
	if got.WarehouseCode != "W2" || got.WarehouseName != "คลังสำรอง" || got.LocationCode != "S2" || got.LocationName != "ชั้นสอง" || got.UnitCode != "ชิ้น" || got.BalanceQty != -3 {
		t.Fatalf("excluded location = %#v", got)
	}
	if states[0].items["A"].RawBalanceQty != 10 || states[0].items["A"].ExcludedBalanceQty != 9 || states[0].items["B"].ExcludedBalanceQty != -12 {
		t.Fatalf("item balances = A:%#v B:%#v", states[0].items["A"], states[0].items["B"])
	}
	itemALocations := sortedNonZeroStockLocations(states[0].itemExcludedLocations["A"])
	if len(itemALocations) != 2 || itemALocations[0].WarehouseCode != "W2" || itemALocations[0].BalanceQty != 5 || itemALocations[1].WarehouseCode != "W3" || itemALocations[1].BalanceQty != 4 {
		t.Fatalf("item A excluded locations = %#v", itemALocations)
	}
}

func TestItemExcludedLocationsAreOptIn(t *testing.T) {
	states := []stockBalanceScopeState{newStockBalanceScopeState(StockBalanceScopeRequest{
		ScopeID: "shop:1", ScopeMode: "selected", ItemCodes: []string{"A"},
		Locations: []StockLocationPair{{Warehouse: "W1", Location: "S1"}},
	})}
	accumulateStockBalanceRow(states, stockBalanceRow{ItemCode: "A", WarehouseCode: "W2", LocationCode: "S2", UnitCode: "ชิ้น", BalanceQty: 2})
	if len(states[0].itemExcludedLocations) != 0 {
		t.Fatalf("item-level location detail must stay opt-in: %#v", states[0].itemExcludedLocations)
	}
}

func TestStockCatalogOrdersUnitsBySMLPriority(t *testing.T) {
	lower := strings.ToLower(stockCatalogSQL)
	if !strings.Contains(lower, "order by u.row_order, u.line_number, u.code") {
		t.Fatal("catalog units must use row_order, line_number, code")
	}
	if !strings.Contains(lower, "coalesce(i.item_type, 0) = any(@item_types::smallint[])") {
		t.Fatal("catalog item types must be opt-in through a parameterized list")
	}
	if !strings.Contains(lower, "unit_standard_stand_value") || !strings.Contains(lower, "unit_standard_divide_value") {
		t.Fatal("catalog must preserve explicit SML standard-unit conversion when ic_unit_use has no row")
	}
}

func TestStockCatalogAcceptsObservedUnitUseStatusConventions(t *testing.T) {
	lower := strings.ToLower(stockCatalogSQL)
	if !strings.Contains(lower, "coalesce(u.status, 0) in (0, 1)") {
		t.Fatal("catalog must include ic_unit_use rows from both observed SML status conventions")
	}
	if !strings.Contains(lower, "coalesce(existing.status, 0) in (0, 1)") {
		t.Fatal("standard-unit fallback must detect units from both observed SML status conventions")
	}
}

func TestStockLocationDiagnosticsOnlyCountActiveStockItems(t *testing.T) {
	lower := strings.ToLower(stockLocationDiagnosticsSQL)
	if !strings.Contains(lower, "join public.ic_inventory") || !strings.Contains(lower, "coalesce(i.item_type, 0) = 0") || !strings.Contains(lower, "coalesce(i.status, 0) = 0") {
		t.Fatal("location diagnostics must match active item_type=0 stock scope")
	}
}

func TestStockCatalogPageParamsAllowsProductionBatchSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/ic/stock-catalog?page=3&size=500", nil)
	page, size := stockCatalogPageParams(ctx)
	if page != 3 || size != 500 {
		t.Fatalf("page=%d size=%d, want 3/500", page, size)
	}
}
