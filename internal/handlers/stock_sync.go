package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/setproducts"
)

const (
	stockBatchMaxScopes        = 20
	stockBatchMaxItemCodes     = 50_000
	stockBatchMaxLocationPairs = 1_000
	stockQueryTimeout          = 10 * time.Second
	stockLocationTimeout       = 15 * time.Second
)

const stockBalanceRowsSQL = `SELECT
	b.ic_code,
	COALESCE(b.ic_name, ''),
	TRIM(COALESCE(b.warehouse, '')),
	COALESCE(w.name_1, ''),
	TRIM(COALESCE(b.location, '')),
	COALESCE(s.name_1, ''),
	COALESCE(b.min_qty, 0)::float8,
	COALESCE(b.max_qty, 0)::float8,
	COALESCE(b.balance_qty, 0)::float8,
	COALESCE(b.ic_unit_code, '')
FROM public.sml_ic_function_stock_balance_warehouse_location($1::date, '', '', '') b
JOIN public.ic_inventory i ON i.code = b.ic_code
LEFT JOIN public.ic_warehouse w
  ON w.code = TRIM(COALESCE(b.warehouse, ''))
LEFT JOIN public.ic_shelf s
  ON s.whcode = TRIM(COALESCE(b.warehouse, ''))
 AND s.code = TRIM(COALESCE(b.location, ''))
WHERE COALESCE(i.status, 0) = 0
  AND COALESCE(i.item_type, 0) = 0
  AND b.ic_code = ANY($2::text[])
ORDER BY b.ic_code, b.warehouse, b.location`

const stockLocationsSQL = `SELECT
	w.code,
	COALESCE(w.name_1, ''),
	COALESCE(s.code, ''),
	COALESCE(s.name_1, '')
FROM public.ic_warehouse w
LEFT JOIN public.ic_shelf s
  ON s.whcode = w.code
 AND COALESCE(s.status, 0) = 0
WHERE COALESCE(w.status, 0) = 0
ORDER BY w.code, s.code`

const stockLocationDiagnosticsSQL = `SELECT
	TRIM(COALESCE(b.warehouse, '')) AS warehouse_code,
	TRIM(COALESCE(b.location, '')) AS location_code,
	SUM(COALESCE(b.balance_qty, 0))::float8 AS balance_qty,
	CASE
		WHEN TRIM(COALESCE(b.warehouse, '')) = '' OR TRIM(COALESCE(b.location, '')) = '' THEN 'blank_location'
		WHEN w.code IS NULL OR s.code IS NULL THEN 'orphan_location'
		ELSE ''
	END AS diagnostic
FROM public.sml_ic_function_stock_balance_warehouse_location($1::date, '', '', '') b
JOIN public.ic_inventory i
  ON i.code = b.ic_code
 AND COALESCE(i.status, 0) = 0
 AND COALESCE(i.item_type, 0) = 0
LEFT JOIN public.ic_warehouse w
  ON w.code = TRIM(COALESCE(b.warehouse, ''))
 AND COALESCE(w.status, 0) = 0
LEFT JOIN public.ic_shelf s
  ON s.whcode = TRIM(COALESCE(b.warehouse, ''))
 AND s.code = TRIM(COALESCE(b.location, ''))
 AND COALESCE(s.status, 0) = 0
GROUP BY TRIM(COALESCE(b.warehouse, '')), TRIM(COALESCE(b.location, '')), w.code, s.code
HAVING SUM(COALESCE(b.balance_qty, 0)) <> 0
   AND (
	TRIM(COALESCE(b.warehouse, '')) = ''
	OR TRIM(COALESCE(b.location, '')) = ''
	OR w.code IS NULL
	OR s.code IS NULL
   )
ORDER BY warehouse_code, location_code`

const stockCatalogCountSQL = `SELECT COUNT(*)
FROM public.ic_inventory i
WHERE COALESCE(i.status, 0) = 0
  AND COALESCE(i.item_type, 0) = ANY(@item_types::smallint[])
  AND (@updated_from::timestamptz IS NULL OR COALESCE(i.last_update_date_time, i.create_datetime, i.create_date_time_now) >= @updated_from)
  AND (@updated_to::timestamptz IS NULL OR COALESCE(i.last_update_date_time, i.create_datetime, i.create_date_time_now) <= @updated_to)`

const stockCatalogSQL = `WITH page AS (
	SELECT
		i.code,
		COALESCE(i.name_1, '') AS name_1,
		COALESCE(i.item_type, 0)::int AS item_type,
		COALESCE(i.unit_standard, '') AS unit_standard,
		COALESCE(i.unit_standard_stand_value, 0)::float8 AS standard_stand_value,
		COALESCE(i.unit_standard_divide_value, 0)::float8 AS standard_divide_value,
		COALESCE(i.last_update_date_time, i.create_datetime, i.create_date_time_now) AS updated_at
	FROM public.ic_inventory i
	WHERE COALESCE(i.status, 0) = 0
	  AND COALESCE(i.item_type, 0) = ANY(@item_types::smallint[])
	  AND (@updated_from::timestamptz IS NULL OR COALESCE(i.last_update_date_time, i.create_datetime, i.create_date_time_now) >= @updated_from)
	  AND (@updated_to::timestamptz IS NULL OR COALESCE(i.last_update_date_time, i.create_datetime, i.create_date_time_now) <= @updated_to)
	ORDER BY COALESCE(i.last_update_date_time, i.create_datetime, i.create_date_time_now), i.code
	LIMIT @size OFFSET @offset
), units AS (
	SELECT
		p.code AS item_code,
		u.code,
		COALESCE(NULLIF(master.name_1, ''), u.code) AS name,
		COALESCE(u.stand_value, 0)::float8 AS stand_value,
		COALESCE(u.divide_value, 0)::float8 AS divide_value,
		COALESCE(u.ratio, 0)::float8 AS ratio,
		COALESCE(u.row_order, 2147483647) AS row_order,
		COALESCE(u.line_number, 2147483647) AS line_number
	FROM page p
	JOIN public.ic_unit_use u ON u.ic_code = p.code AND COALESCE(u.status, 0) = 0
	LEFT JOIN public.ic_unit master ON master.code = u.code
	UNION ALL
	SELECT
		p.code,
		p.unit_standard,
		COALESCE(NULLIF(master.name_1, ''), p.unit_standard),
		p.standard_stand_value,
		p.standard_divide_value,
		CASE WHEN p.standard_divide_value = 0 THEN 0 ELSE p.standard_stand_value / p.standard_divide_value END,
		2147483647,
		2147483647
	FROM page p
	LEFT JOIN public.ic_unit master ON master.code = p.unit_standard
	WHERE p.unit_standard <> ''
	  AND NOT EXISTS (
		SELECT 1 FROM public.ic_unit_use existing
		WHERE existing.ic_code = p.code
		  AND COALESCE(existing.status, 0) = 0
		  AND existing.code = p.unit_standard
	  )
), barcodes AS (
	SELECT
		p.code AS item_code,
		b.barcode,
		COALESCE(b.unit_code, '') AS unit_code,
		COALESCE(b.wh_code, '') AS warehouse,
		COALESCE(b.shelf_code, '') AS location,
		COALESCE(b.line_number, 2147483647) AS line_number
	FROM page p
	JOIN public.ic_inventory_barcode b ON b.ic_code = p.code
	WHERE COALESCE(b.status, 0) = 0 AND TRIM(COALESCE(b.barcode, '')) <> ''
)
SELECT
	p.code,
	p.name_1,
	p.item_type,
	p.unit_standard,
	COALESCE(p.updated_at, TIMESTAMP '1970-01-01'),
	COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'code', u.code,
			'name', u.name,
			'stand_value', u.stand_value,
			'divide_value', u.divide_value,
			'ratio', u.ratio,
			'row_order', u.row_order,
			'line_number', u.line_number
		) ORDER BY u.row_order, u.line_number, u.code)
		FROM units u WHERE u.item_code = p.code
	), '[]'::jsonb),
	COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'barcode', b.barcode,
			'unit_code', b.unit_code,
			'warehouse', b.warehouse,
			'location', b.location
		) ORDER BY b.line_number, b.barcode)
		FROM barcodes b WHERE b.item_code = p.code
	), '[]'::jsonb)
FROM page p
ORDER BY p.updated_at, p.code`

type stockSyncQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type StockSyncHandler struct {
	getPool      func(context.Context, string) (stockSyncQuerier, error)
	global       chan struct{}
	tenantLocks  sync.Map
	singleflight singleflight.Group
}

type StockLocationPair struct {
	Warehouse string `json:"warehouse"`
	Location  string `json:"location"`
}

type StockBalanceScopeRequest struct {
	ScopeID                      string              `json:"scope_id"`
	ItemCodes                    []string            `json:"item_codes"`
	ScopeMode                    string              `json:"scope_mode"`
	Locations                    []StockLocationPair `json:"locations,omitempty"`
	IncludeItemExcludedLocations bool                `json:"include_item_excluded_locations,omitempty"`
}

type StockBalanceBatchRequest struct {
	AsOfDate string                     `json:"as_of_date"`
	Scopes   []StockBalanceScopeRequest `json:"scopes"`
}

type StockBalanceItem struct {
	ItemCode           string                 `json:"item_code"`
	ItemName           string                 `json:"item_name,omitempty"`
	UnitCode           string                 `json:"unit_code,omitempty"`
	RawBalanceQty      float64                `json:"raw_balance_qty"`
	BalanceQty         float64                `json:"balance_qty"`
	ExcludedBalanceQty float64                `json:"excluded_balance_qty,omitempty"`
	ExcludedLocations  []StockBalanceLocation `json:"excluded_locations,omitempty"`
	MinQty             float64                `json:"min_qty"`
	MaxQty             float64                `json:"max_qty"`
	NegativeClamped    bool                   `json:"negative_clamped,omitempty"`
}

// StockBalanceLocation explains balances outside a selected scope without
// exposing item-level rows. This keeps the shared API response bounded while
// allowing clients to show users which SML warehouse/location holds the stock.
type StockBalanceLocation struct {
	WarehouseCode string  `json:"warehouse_code"`
	WarehouseName string  `json:"warehouse_name,omitempty"`
	LocationCode  string  `json:"location_code"`
	LocationName  string  `json:"location_name,omitempty"`
	UnitCode      string  `json:"unit_code,omitempty"`
	BalanceQty    float64 `json:"balance_qty"`
}

type StockBalanceScopeResult struct {
	ScopeID           string                 `json:"scope_id"`
	Items             []StockBalanceItem     `json:"items"`
	ExcludedLocations []StockBalanceLocation `json:"excluded_locations"`
}

type StockBalanceBatchResponse struct {
	AsOfDate  string                    `json:"as_of_date"`
	Scopes    []StockBalanceScopeResult `json:"scopes"`
	CheckedAt string                    `json:"checked_at"`
}

type StockCatalogUnit struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	StandValue  float64 `json:"stand_value"`
	DivideValue float64 `json:"divide_value"`
	Ratio       float64 `json:"ratio"`
	RowOrder    int     `json:"row_order"`
	LineNumber  int     `json:"line_number"`
}

type StockCatalogBarcode struct {
	Barcode   string `json:"barcode"`
	UnitCode  string `json:"unit_code"`
	Warehouse string `json:"warehouse"`
	Location  string `json:"location"`
}

type StockCatalogItem struct {
	ItemCode      string                  `json:"item_code"`
	ItemName      string                  `json:"item_name"`
	ItemType      int                     `json:"item_type"`
	StandardUnit  string                  `json:"standard_unit"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Units         []StockCatalogUnit      `json:"units"`
	Barcodes      []StockCatalogBarcode   `json:"barcodes"`
	SetDefinition *setproducts.Definition `json:"set_definition,omitempty"`
	SetComponents []setproducts.Component `json:"set_components,omitempty"`
}

type StockLocation struct {
	WarehouseCode string `json:"warehouse_code"`
	WarehouseName string `json:"warehouse_name"`
	LocationCode  string `json:"location_code"`
	LocationName  string `json:"location_name"`
}

type StockLocationDiagnostic struct {
	Warehouse string  `json:"warehouse"`
	Location  string  `json:"location"`
	Balance   float64 `json:"balance_qty"`
	Code      string  `json:"code"`
}

func NewStockSyncHandler(dbm *db.Manager) *StockSyncHandler {
	return &StockSyncHandler{
		getPool: func(ctx context.Context, tenant string) (stockSyncQuerier, error) {
			return dbm.Get(ctx, tenant)
		},
		global: make(chan struct{}, 5),
	}
}

func (h *StockSyncHandler) BalancesBatch(c *gin.Context) {
	var req StockBalanceBatchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		api.BadRequest(c, "invalid_stock_request", "stock balance request is invalid", nil)
		return
	}
	normalized, itemCodes, err := normalizeStockBatchRequest(req)
	if err != nil {
		api.BadRequest(c, "invalid_stock_request", err.Error(), nil)
		return
	}
	tenant := c.GetString(middleware.TenantKey)
	requestJSON, _ := json.Marshal(normalized)
	hash := sha256.Sum256(requestJSON)
	key := tenant + ":" + hex.EncodeToString(hash[:])

	result, err, _ := h.singleflight.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), stockQueryTimeout)
		defer cancel()
		release, err := h.acquire(ctx, tenant)
		if err != nil {
			return nil, err
		}
		defer release()
		pool, err := h.getPool(ctx, tenant)
		if err != nil {
			return nil, err
		}
		return calculateStockScopes(ctx, pool, normalized, itemCodes)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			api.Error(c, http.StatusGatewayTimeout, "stock_calculation_timeout", "stock calculation timed out", nil)
			return
		}
		api.Internal(c, "stock_calculation_failed", "calculate stock balances failed", err.Error())
		return
	}
	api.OK(c, result)
}

func (h *StockSyncHandler) Locations(c *gin.Context) {
	asOfDate, err := parseStockDate(c.DefaultQuery("as_of_date", stockTodayBangkok()))
	if err != nil {
		api.BadRequest(c, "invalid_as_of_date", err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), stockLocationTimeout)
	defer cancel()
	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}
	rows, err := pool.Query(ctx, stockLocationsSQL)
	if err != nil {
		api.Internal(c, "stock_locations_failed", "list stock locations failed", err.Error())
		return
	}
	locations := make([]StockLocation, 0)
	for rows.Next() {
		var item StockLocation
		if err := rows.Scan(&item.WarehouseCode, &item.WarehouseName, &item.LocationCode, &item.LocationName); err != nil {
			rows.Close()
			api.Internal(c, "stock_locations_scan_failed", "read stock location failed", err.Error())
			return
		}
		locations = append(locations, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		api.Internal(c, "stock_locations_failed", "list stock locations failed", err.Error())
		return
	}
	rows.Close()

	diagnostics := make([]StockLocationDiagnostic, 0)
	diagnosticRows, err := pool.Query(ctx, stockLocationDiagnosticsSQL, asOfDate)
	if err != nil {
		api.Internal(c, "stock_location_diagnostics_failed", "inspect stock locations failed", err.Error())
		return
	}
	defer diagnosticRows.Close()
	for diagnosticRows.Next() {
		var item StockLocationDiagnostic
		if err := diagnosticRows.Scan(&item.Warehouse, &item.Location, &item.Balance, &item.Code); err != nil {
			api.Internal(c, "stock_location_diagnostics_scan_failed", "read stock location diagnostic failed", err.Error())
			return
		}
		diagnostics = append(diagnostics, item)
	}
	if err := diagnosticRows.Err(); err != nil {
		api.Internal(c, "stock_location_diagnostics_failed", "inspect stock locations failed", err.Error())
		return
	}
	api.OK(c, gin.H{
		"as_of_date":  asOfDate.Format("2006-01-02"),
		"locations":   locations,
		"diagnostics": diagnostics,
		"checked_at":  time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *StockSyncHandler) Catalog(c *gin.Context) {
	page, size := stockCatalogPageParams(c)
	includeSets, err := strconv.ParseBool(c.DefaultQuery("include_sets", "false"))
	if err != nil {
		api.BadRequest(c, "invalid_include_sets", "include_sets must be true or false", nil)
		return
	}
	updatedFrom, err := parseOptionalTime(c.Query("updated_from"))
	if err != nil {
		api.BadRequest(c, "invalid_updated_from", "updated_from must be RFC3339 or YYYY-MM-DD", nil)
		return
	}
	updatedTo, err := parseOptionalTime(c.Query("updated_to"))
	if err != nil {
		api.BadRequest(c, "invalid_updated_to", "updated_to must be RFC3339 or YYYY-MM-DD", nil)
		return
	}
	if updatedFrom != nil && updatedTo != nil && updatedTo.Before(*updatedFrom) {
		api.BadRequest(c, "invalid_updated_range", "updated_to must not be before updated_from", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), stockQueryTimeout)
	defer cancel()
	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}
	itemTypes := []int16{0}
	if includeSets {
		itemTypes = append(itemTypes, 3)
	}
	args := pgx.NamedArgs{"updated_from": updatedFrom, "updated_to": updatedTo, "item_types": itemTypes, "size": size, "offset": (page - 1) * size}
	var total int
	if err := pool.QueryRow(ctx, stockCatalogCountSQL, args).Scan(&total); err != nil {
		api.Internal(c, "stock_catalog_count_failed", "count stock catalog failed", err.Error())
		return
	}
	rows, err := pool.Query(ctx, stockCatalogSQL, args)
	if err != nil {
		api.Internal(c, "stock_catalog_failed", "list stock catalog failed", err.Error())
		return
	}
	defer rows.Close()
	items := make([]StockCatalogItem, 0, size)
	for rows.Next() {
		var item StockCatalogItem
		var unitsJSON, barcodesJSON []byte
		if err := rows.Scan(&item.ItemCode, &item.ItemName, &item.ItemType, &item.StandardUnit, &item.UpdatedAt, &unitsJSON, &barcodesJSON); err != nil {
			api.Internal(c, "stock_catalog_scan_failed", "read stock catalog failed", err.Error())
			return
		}
		if err := json.Unmarshal(unitsJSON, &item.Units); err != nil {
			api.Internal(c, "stock_catalog_units_invalid", "read stock catalog units failed", err.Error())
			return
		}
		if err := json.Unmarshal(barcodesJSON, &item.Barcodes); err != nil {
			api.Internal(c, "stock_catalog_barcodes_invalid", "read stock catalog barcodes failed", err.Error())
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "stock_catalog_failed", "list stock catalog failed", err.Error())
		return
	}
	if includeSets {
		setCodes := make([]string, 0)
		for i := range items {
			if items[i].ItemType == 3 {
				setCodes = append(setCodes, items[i].ItemCode)
			}
		}
		definitions, err := setproducts.LoadDefinitions(ctx, pool, c.GetString(middleware.TenantKey), setCodes)
		if err != nil {
			writeSetProductError(c, err)
			return
		}
		for i := range items {
			definition, ok := definitions[items[i].ItemCode]
			if !ok {
				continue
			}
			definitionCopy := definition
			items[i].SetDefinition = &definitionCopy
			items[i].SetComponents = append([]setproducts.Component(nil), definition.Components...)
		}
	}
	api.OKPage(c, items, total, page, size)
}

func stockCatalogPageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "100"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 100
	}
	return page, size
}

func normalizeStockBatchRequest(req StockBalanceBatchRequest) (StockBalanceBatchRequest, []string, error) {
	date, err := parseStockDate(req.AsOfDate)
	if err != nil {
		return StockBalanceBatchRequest{}, nil, err
	}
	if len(req.Scopes) == 0 || len(req.Scopes) > stockBatchMaxScopes {
		return StockBalanceBatchRequest{}, nil, fmt.Errorf("scopes must contain 1-%d entries", stockBatchMaxScopes)
	}
	uniqueItems := map[string]struct{}{}
	uniqueScopes := map[string]struct{}{}
	locationCount := 0
	for i := range req.Scopes {
		scope := &req.Scopes[i]
		scope.ScopeID = strings.TrimSpace(scope.ScopeID)
		scope.ScopeMode = strings.ToLower(strings.TrimSpace(scope.ScopeMode))
		if scope.ScopeID == "" || len(scope.ScopeID) > 100 {
			return StockBalanceBatchRequest{}, nil, errors.New("scope_id is required and must not exceed 100 characters")
		}
		if _, exists := uniqueScopes[scope.ScopeID]; exists {
			return StockBalanceBatchRequest{}, nil, fmt.Errorf("duplicate scope_id %q", scope.ScopeID)
		}
		uniqueScopes[scope.ScopeID] = struct{}{}
		if scope.ScopeMode != "all" && scope.ScopeMode != "selected" {
			return StockBalanceBatchRequest{}, nil, fmt.Errorf("scope %q must use scope_mode all or selected", scope.ScopeID)
		}
		itemSet := map[string]struct{}{}
		for _, raw := range scope.ItemCodes {
			code := strings.TrimSpace(raw)
			if code == "" || len(code) > 100 || strings.ContainsAny(code, "\x00\r\n") {
				return StockBalanceBatchRequest{}, nil, fmt.Errorf("scope %q contains an invalid item code", scope.ScopeID)
			}
			itemSet[code] = struct{}{}
			uniqueItems[code] = struct{}{}
		}
		if len(itemSet) == 0 {
			return StockBalanceBatchRequest{}, nil, fmt.Errorf("scope %q must contain at least one item code", scope.ScopeID)
		}
		scope.ItemCodes = sortedKeys(itemSet)
		locationSet := map[string]StockLocationPair{}
		for _, raw := range scope.Locations {
			pair := StockLocationPair{Warehouse: strings.TrimSpace(raw.Warehouse), Location: strings.TrimSpace(raw.Location)}
			if pair.Warehouse == "" || pair.Location == "" || len(pair.Warehouse) > 100 || len(pair.Location) > 100 || strings.ContainsAny(pair.Warehouse+pair.Location, "\x00\r\n") {
				return StockBalanceBatchRequest{}, nil, fmt.Errorf("scope %q contains an invalid warehouse/location pair", scope.ScopeID)
			}
			locationSet[locationKey(pair.Warehouse, pair.Location)] = pair
		}
		if scope.ScopeMode == "selected" && len(locationSet) == 0 {
			return StockBalanceBatchRequest{}, nil, fmt.Errorf("scope %q must select at least one warehouse/location pair", scope.ScopeID)
		}
		if scope.ScopeMode == "all" && len(locationSet) > 0 {
			return StockBalanceBatchRequest{}, nil, fmt.Errorf("scope %q cannot include locations in all mode", scope.ScopeID)
		}
		keys := make([]string, 0, len(locationSet))
		for key := range locationSet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		scope.Locations = scope.Locations[:0]
		for _, key := range keys {
			scope.Locations = append(scope.Locations, locationSet[key])
		}
		locationCount += len(scope.Locations)
	}
	if len(uniqueItems) > stockBatchMaxItemCodes {
		return StockBalanceBatchRequest{}, nil, fmt.Errorf("request exceeds %d unique item codes", stockBatchMaxItemCodes)
	}
	if locationCount > stockBatchMaxLocationPairs {
		return StockBalanceBatchRequest{}, nil, fmt.Errorf("request exceeds %d warehouse/location pairs", stockBatchMaxLocationPairs)
	}
	req.AsOfDate = date.Format("2006-01-02")
	return req, sortedKeys(uniqueItems), nil
}

type stockBalanceRow struct {
	ItemCode      string
	ItemName      string
	WarehouseCode string
	WarehouseName string
	LocationCode  string
	LocationName  string
	MinQty        float64
	MaxQty        float64
	BalanceQty    float64
	UnitCode      string
}

type stockBalanceScopeState struct {
	request               StockBalanceScopeRequest
	items                 map[string]*StockBalanceItem
	selected              map[string]struct{}
	excludedLocations     map[string]*StockBalanceLocation
	itemExcludedLocations map[string]map[string]*StockBalanceLocation
}

func newStockBalanceScopeState(scope StockBalanceScopeRequest) stockBalanceScopeState {
	state := stockBalanceScopeState{
		request: scope, items: map[string]*StockBalanceItem{}, selected: map[string]struct{}{},
		excludedLocations: map[string]*StockBalanceLocation{}, itemExcludedLocations: map[string]map[string]*StockBalanceLocation{},
	}
	for _, code := range scope.ItemCodes {
		state.items[code] = &StockBalanceItem{ItemCode: code}
	}
	for _, pair := range scope.Locations {
		state.selected[locationKey(pair.Warehouse, pair.Location)] = struct{}{}
	}
	return state
}

func accumulateStockBalanceRow(states []stockBalanceScopeState, row stockBalanceRow) {
	key := locationKey(row.WarehouseCode, row.LocationCode)
	for i := range states {
		item := states[i].items[row.ItemCode]
		if item == nil {
			continue
		}
		item.ItemName = row.ItemName
		item.UnitCode = row.UnitCode
		included := states[i].request.ScopeMode == "all"
		if !included {
			_, included = states[i].selected[key]
		}
		if included {
			item.RawBalanceQty += row.BalanceQty
			item.MinQty += row.MinQty
			item.MaxQty += row.MaxQty
			continue
		}
		item.ExcludedBalanceQty += row.BalanceQty
		balanceKey := key + "\x00" + strings.TrimSpace(row.UnitCode)
		location := states[i].excludedLocations[balanceKey]
		if location == nil {
			location = &StockBalanceLocation{
				WarehouseCode: row.WarehouseCode, WarehouseName: row.WarehouseName,
				LocationCode: row.LocationCode, LocationName: row.LocationName, UnitCode: row.UnitCode,
			}
			states[i].excludedLocations[balanceKey] = location
		}
		location.BalanceQty += row.BalanceQty
		if states[i].request.IncludeItemExcludedLocations {
			itemLocations := states[i].itemExcludedLocations[row.ItemCode]
			if itemLocations == nil {
				itemLocations = map[string]*StockBalanceLocation{}
				states[i].itemExcludedLocations[row.ItemCode] = itemLocations
			}
			itemLocation := itemLocations[balanceKey]
			if itemLocation == nil {
				itemLocation = &StockBalanceLocation{
					WarehouseCode: row.WarehouseCode, WarehouseName: row.WarehouseName,
					LocationCode: row.LocationCode, LocationName: row.LocationName, UnitCode: row.UnitCode,
				}
				itemLocations[balanceKey] = itemLocation
			}
			itemLocation.BalanceQty += row.BalanceQty
		}
	}
}

func sortedNonZeroStockLocations(values map[string]*StockBalanceLocation) []StockBalanceLocation {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value != nil && math.Abs(value.BalanceQty) > 1e-9 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := make([]StockBalanceLocation, 0, len(keys))
	for _, key := range keys {
		result = append(result, *values[key])
	}
	return result
}

func calculateStockScopes(ctx context.Context, pool stockSyncQuerier, req StockBalanceBatchRequest, itemCodes []string) (*StockBalanceBatchResponse, error) {
	asOfDate, _ := time.Parse("2006-01-02", req.AsOfDate)
	states := make([]stockBalanceScopeState, len(req.Scopes))
	for i, scope := range req.Scopes {
		states[i] = newStockBalanceScopeState(scope)
	}
	rows, err := pool.Query(ctx, stockBalanceRowsSQL, asOfDate, itemCodes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row stockBalanceRow
		if err := rows.Scan(
			&row.ItemCode, &row.ItemName,
			&row.WarehouseCode, &row.WarehouseName,
			&row.LocationCode, &row.LocationName,
			&row.MinQty, &row.MaxQty, &row.BalanceQty, &row.UnitCode,
		); err != nil {
			return nil, err
		}
		accumulateStockBalanceRow(states, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	response := &StockBalanceBatchResponse{AsOfDate: req.AsOfDate, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	response.Scopes = make([]StockBalanceScopeResult, 0, len(states))
	for _, state := range states {
		result := StockBalanceScopeResult{
			ScopeID: state.request.ScopeID, Items: make([]StockBalanceItem, 0, len(state.request.ItemCodes)),
			ExcludedLocations: sortedNonZeroStockLocations(state.excludedLocations),
		}
		for _, code := range state.request.ItemCodes {
			item := state.items[code]
			if state.request.IncludeItemExcludedLocations {
				item.ExcludedLocations = sortedNonZeroStockLocations(state.itemExcludedLocations[code])
			}
			item.BalanceQty = item.RawBalanceQty
			if item.BalanceQty < 0 {
				item.BalanceQty = 0
				item.NegativeClamped = true
			}
			result.Items = append(result.Items, *item)
		}
		response.Scopes = append(response.Scopes, result)
	}
	return response, nil
}

func (h *StockSyncHandler) acquire(ctx context.Context, tenant string) (func(), error) {
	value, _ := h.tenantLocks.LoadOrStore(tenant, make(chan struct{}, 1))
	tenantSem := value.(chan struct{})
	select {
	case h.global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case tenantSem <- struct{}{}:
		return func() { <-tenantSem; <-h.global }, nil
	case <-ctx.Done():
		<-h.global
		return nil, ctx.Err()
	}
}

func parseStockDate(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, errors.New("as_of_date must use YYYY-MM-DD")
	}
	today, _ := time.Parse("2006-01-02", stockTodayBangkok())
	if date.After(today) {
		return time.Time{}, errors.New("as_of_date cannot be in the future")
	}
	return date, nil
}

func stockTodayBangkok() string {
	return time.Now().In(time.FixedZone("Asia/Bangkok", 7*60*60)).Format("2006-01-02")
}

func parseOptionalTime(raw string) (*time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func locationKey(warehouse, location string) string {
	return strings.TrimSpace(warehouse) + "\x00" + strings.TrimSpace(location)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var _ stockSyncQuerier = (*pgxpool.Pool)(nil)
