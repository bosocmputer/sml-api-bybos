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
		AsOfDate: "2026-07-01",
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
		ScopeID:   "shop:1",
		ScopeMode: "selected",
		ItemCodes: []string{"A", "B"},
		Locations: []StockLocationPair{{Warehouse: "W1", Location: "S1"}},
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
