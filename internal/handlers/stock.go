package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

type StockHandler struct {
	dbm *db.Manager
}

func NewStockHandler(dbm *db.Manager) *StockHandler {
	return &StockHandler{dbm: dbm}
}

type warehouseStock struct {
	Warehouse  string  `json:"warehouse"`
	Location   string  `json:"location"`
	BalanceQty float64 `json:"balance_qty"`
	AvgCost    float64 `json:"average_cost"`
}

type stockItem struct {
	ItemCode    string           `json:"item_code"`
	ItemName    string           `json:"item_name"`
	UnitCode    string           `json:"unit_code"`
	TotalQty    float64          `json:"total_qty"`
	AverageCost float64          `json:"average_cost"`
	Warehouses  []warehouseStock `json:"warehouses"`
}

// GET /api/v1/ic/stock
// Query params: code (filter by item code), page, size
// Uses vw_stock_balance_by_sloc (SML's own pre-calculated view).
func (h *StockHandler) List(c *gin.Context) {
	code := c.Query("code")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	where := "WHERE balance_qty > 0"
	args := []interface{}{}
	argIdx := 1
	if code != "" {
		where += " AND ic_code = $1"
		args = append(args, code)
		argIdx++
	}

	// Aggregate per item first, then join warehouse breakdown
	query := `
		SELECT ic_code, MAX(ic_name), MAX(ic_unit_code),
		       SUM(balance_qty) AS total_qty,
		       MAX(average_cost),
		       COUNT(*) OVER() AS total_count
		FROM vw_stock_balance_by_sloc
		` + where + `
		GROUP BY ic_code
		ORDER BY ic_code
		LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, size, offset)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type stockRow struct {
		code string
		item stockItem
	}
	var items []stockRow
	var codes []interface{}
	var total int

	for rows.Next() {
		var r stockRow
		if err := rows.Scan(&r.code, &r.item.ItemName, &r.item.UnitCode,
			&r.item.TotalQty, &r.item.AverageCost, &total); err != nil {
			rows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		r.item.ItemCode = r.code
		r.item.Warehouses = []warehouseStock{}
		items = append(items, r)
		codes = append(codes, r.code)
	}
	rows.Close()

	// Fetch warehouse breakdown for returned items in one query
	if len(codes) > 0 {
		placeholders := "$1"
		for i := 1; i < len(codes); i++ {
			placeholders += ", $" + strconv.Itoa(i+1)
		}
		whRows, err := pool.Query(ctx,
			`SELECT ic_code, COALESCE(warehouse,''), COALESCE(location,''), balance_qty, average_cost
			 FROM vw_stock_balance_by_sloc
			 WHERE ic_code IN (`+placeholders+`) AND balance_qty > 0
			 ORDER BY ic_code, warehouse, location`, codes...)
		if err == nil {
			whMap := map[string][]warehouseStock{}
			for whRows.Next() {
				var ic string
				var wh warehouseStock
				_ = whRows.Scan(&ic, &wh.Warehouse, &wh.Location, &wh.BalanceQty, &wh.AvgCost)
				whMap[ic] = append(whMap[ic], wh)
			}
			whRows.Close()
			for i := range items {
				if whs, ok := whMap[items[i].code]; ok {
					items[i].item.Warehouses = whs
				}
			}
		}
	}

	result := make([]stockItem, len(items))
	for i, r := range items {
		result[i] = r.item
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  result,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// GET /api/v1/ic/stock/:code — detail for one item
func (h *StockHandler) Get(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := pool.Query(ctx,
		`SELECT COALESCE(warehouse,''), COALESCE(location,''),
		        COALESCE(ic_name,''), COALESCE(ic_unit_code,''),
		        balance_qty, average_cost
		 FROM vw_stock_balance_by_sloc
		 WHERE ic_code = $1
		 ORDER BY warehouse, location`, code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	item := stockItem{ItemCode: code, Warehouses: []warehouseStock{}}
	found := false
	for rows.Next() {
		var wh warehouseStock
		_ = rows.Scan(&wh.Warehouse, &wh.Location, &item.ItemName, &item.UnitCode,
			&wh.BalanceQty, &wh.AvgCost)
		item.TotalQty += wh.BalanceQty
		item.AverageCost = wh.AvgCost
		item.Warehouses = append(item.Warehouses, wh)
		found = true
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found or no stock"})
		return
	}
	c.JSON(http.StatusOK, item)
}
