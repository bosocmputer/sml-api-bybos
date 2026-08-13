package setproducts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Set expansion is tenant-generic and opt-in because sml-api-bybos is shared
// by multiple projects. Never put customer-specific product rules here.
var ErrSchemaUnsupported = errors.New("SML set-product schema is not supported by this tenant")

type Queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Product struct {
	Code       string
	Name       string
	ItemType   int
	UnitCode   string
	Active     bool
	Definition *Definition
}

type capabilityEntry struct {
	checkedAt time.Time
	ok        bool
}

var capabilityCache = struct {
	sync.Mutex
	entries map[string]capabilityEntry
}{entries: map[string]capabilityEntry{}}

const capabilityTTL = 5 * time.Minute

func CheckCapability(ctx context.Context, tenant string, q Queryer) error {
	tenant = strings.TrimSpace(tenant)
	capabilityCache.Lock()
	entry, found := capabilityCache.entries[tenant]
	capabilityCache.Unlock()
	if found && time.Since(entry.checkedAt) < capabilityTTL {
		if entry.ok {
			return nil
		}
		return ErrSchemaUnsupported
	}

	setColumns := []string{"ic_set_code", "ic_code", "unit_code", "qty", "status", "line_number", "roworder", "price", "sum_amount", "price_ratio"}
	inventoryColumns := []string{
		"code", "name_1", "item_type", "unit_standard",
		"unit_standard_stand_value", "unit_standard_divide_value", "status",
	}
	unitColumns := []string{"ic_code", "code", "stand_value", "divide_value", "status", "row_order", "line_number"}
	var setCount, inventoryCount, unitCount int
	err := q.QueryRow(ctx, `SELECT
		(SELECT COUNT(DISTINCT column_name) FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='ic_inventory_set_detail' AND column_name=ANY($1::text[])),
		(SELECT COUNT(DISTINCT column_name) FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='ic_inventory' AND column_name=ANY($2::text[])),
		(SELECT COUNT(DISTINCT column_name) FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='ic_unit_use' AND column_name=ANY($3::text[]))`,
		setColumns, inventoryColumns, unitColumns).Scan(&setCount, &inventoryCount, &unitCount)
	ok := err == nil && setCount == len(setColumns) && inventoryCount == len(inventoryColumns) && unitCount == len(unitColumns)
	capabilityCache.Lock()
	capabilityCache.entries[tenant] = capabilityEntry{checkedAt: time.Now(), ok: ok}
	capabilityCache.Unlock()
	if err != nil {
		return fmt.Errorf("probe SML set-product schema: %w", err)
	}
	if !ok {
		return ErrSchemaUnsupported
	}
	return nil
}

func LoadProducts(ctx context.Context, q Queryer, tenant string, itemCodes []string, includeDefinitions bool) (map[string]Product, error) {
	codes := uniqueCodes(itemCodes)
	products := make(map[string]Product, len(codes))
	if len(codes) == 0 {
		return products, nil
	}
	rows, err := q.Query(ctx, `SELECT
		TRIM(i.code), COALESCE(i.name_1, ''), COALESCE(i.item_type, 0)::int,
		TRIM(COALESCE(i.unit_standard, '')), COALESCE(i.status, 0) = 0
		FROM public.ic_inventory i
		WHERE i.code = ANY($1::text[])`, codes)
	if err != nil {
		return nil, fmt.Errorf("load SML products: %w", err)
	}
	defer rows.Close()
	setCodes := []string{}
	for rows.Next() {
		var product Product
		if err := rows.Scan(&product.Code, &product.Name, &product.ItemType, &product.UnitCode, &product.Active); err != nil {
			return nil, fmt.Errorf("scan SML product: %w", err)
		}
		products[product.Code] = product
		if includeDefinitions && product.ItemType == 3 {
			setCodes = append(setCodes, product.Code)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load SML products: %w", err)
	}
	if len(setCodes) == 0 {
		return products, nil
	}
	definitions, err := LoadDefinitions(ctx, q, tenant, setCodes)
	if err != nil {
		return nil, err
	}
	for code, definition := range definitions {
		product := products[code]
		definitionCopy := definition
		product.Definition = &definitionCopy
		products[code] = product
	}
	return products, nil
}

func LoadDefinitions(ctx context.Context, q Queryer, tenant string, itemCodes []string) (map[string]Definition, error) {
	codes := uniqueCodes(itemCodes)
	definitions := make(map[string]Definition, len(codes))
	if len(codes) == 0 {
		return definitions, nil
	}
	if err := CheckCapability(ctx, tenant, q); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT
		TRIM(sd.ic_set_code), COALESCE(sd.line_number, 0)::int,
		COALESCE(sd.roworder, 0)::int, TRIM(COALESCE(sd.ic_code, '')),
		COALESCE(component.name_1, ''), COALESCE(component.item_type, 0)::int,
		TRIM(COALESCE(sd.unit_code, '')), COALESCE(sd.qty, 0)::float8,
		COALESCE(sd.price, 0)::float8, COALESCE(sd.sum_amount, 0)::float8,
		COALESCE(sd.price_ratio, 0)::float8,
		CASE
			WHEN unit_use.divide_value IS NULL OR unit_use.divide_value = 0 THEN 0
			ELSE unit_use.stand_value / unit_use.divide_value
		END::float8 AS unit_factor,
		COALESCE(component.status, 1) = 0 AS active,
		unit_use.code IS NOT NULL AS unit_valid
		FROM public.ic_inventory_set_detail sd
		LEFT JOIN public.ic_inventory component ON component.code = sd.ic_code
		LEFT JOIN LATERAL (
			SELECT candidate.code, candidate.stand_value, candidate.divide_value
			FROM (
				SELECT u.code, COALESCE(u.stand_value, 0)::float8 AS stand_value,
				       COALESCE(u.divide_value, 0)::float8 AS divide_value,
				       0 AS source_priority, COALESCE(u.row_order, 2147483647) AS row_order,
				       COALESCE(u.line_number, 2147483647) AS line_number
				FROM public.ic_unit_use u
				WHERE u.ic_code = sd.ic_code
				  AND u.code = sd.unit_code
				  AND COALESCE(u.status, 0) = 0
				UNION ALL
				-- Some SML tenants mark ic_unit_use inactive while the same unit remains
				-- the inventory standard unit. This tenant-generic fallback mirrors the
				-- stock-catalog contract and never substitutes a different unit code.
				SELECT TRIM(component.unit_standard),
				       COALESCE(component.unit_standard_stand_value, 0)::float8,
				       COALESCE(component.unit_standard_divide_value, 0)::float8,
				       1, 2147483647, 2147483647
				WHERE TRIM(COALESCE(component.unit_standard, '')) = TRIM(COALESCE(sd.unit_code, ''))
			) candidate
			ORDER BY candidate.source_priority, candidate.row_order, candidate.line_number, candidate.code
			LIMIT 1
		) unit_use ON true
		WHERE sd.ic_set_code = ANY($1::text[])
		  AND COALESCE(sd.status, 0) = 0
		ORDER BY sd.ic_set_code, COALESCE(sd.line_number, 0), COALESCE(sd.roworder, 0), sd.ic_code`, codes)
	if err != nil {
		return nil, fmt.Errorf("load SML set definitions: %w", err)
	}
	defer rows.Close()
	grouped := make(map[string][]Component, len(codes))
	for rows.Next() {
		var code string
		var component Component
		if err := rows.Scan(
			&code, &component.LineNumber, &component.RowOrder, &component.ItemCode,
			&component.ItemName, &component.ItemType, &component.UnitCode, &component.Qty,
			&component.Price, &component.SumAmount, &component.PriceRatio,
			&component.UnitFactor, &component.Active, &component.UnitValid,
		); err != nil {
			return nil, fmt.Errorf("scan SML set definition: %w", err)
		}
		grouped[code] = append(grouped[code], component)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load SML set definitions: %w", err)
	}
	for _, code := range codes {
		definitions[code] = BuildDefinition(code, grouped[code])
	}
	return definitions, nil
}

func uniqueCodes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
