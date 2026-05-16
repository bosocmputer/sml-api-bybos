package compat

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
)

type ReadHandler struct {
	dbm *db.Manager
}

func NewReadHandler(dbm *db.Manager) *ReadHandler {
	return &ReadHandler{dbm: dbm}
}

// ─── Party ────────────────────────────────────────────────────────────────────
// GET /SMLJavaRESTService/v3/api/customer?page=1&size=200
// GET /SMLJavaRESTService/v3/api/supplier?page=1&size=200

type partyItem struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	TaxID     string `json:"tax_id"`
	Telephone string `json:"telephone"`
	Address   string `json:"address"`
}

type partyPages struct {
	Size        int `json:"size"`
	Page        int `json:"page"`
	TotalRecord int `json:"total_record"`
	MaxPage     int `json:"max_page"`
}

type partyListResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Code    string      `json:"code"`
	Data    []partyItem `json:"data"`
	Pages   partyPages  `json:"pages"`
}

func (h *ReadHandler) ListCustomers(c *gin.Context) {
	h.listParty(c, "customer")
}

func (h *ReadHandler) ListSuppliers(c *gin.Context) {
	h.listParty(c, "supplier")
}

func (h *ReadHandler) listParty(c *gin.Context, kind string) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "200"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 200
	}
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	var table, detailTable, joinCol string
	if kind == "customer" {
		table, detailTable, joinCol = "ar_customer", "ar_customer_detail", "ar_code"
	} else {
		table, detailTable, joinCol = "ap_supplier", "ap_supplier_detail", "ap_code"
	}

	var total, maxPage int

	rows, err := pool.Query(ctx, `
		SELECT t.code, t.name_1, COALESCE(d.tax_id,''), COALESCE(t.telephone,''), COALESCE(t.address,''),
		       COUNT(*) OVER() AS total_count
		FROM `+table+` t
		LEFT JOIN `+detailTable+` d ON t.code = d.`+joinCol+`
		WHERE t.status=0
		ORDER BY t.code
		LIMIT $1 OFFSET $2`, size, offset)
	if err != nil {
		c.JSON(http.StatusOK, partyListResponse{Success: false, Message: err.Error()})
		return
	}
	defer rows.Close()

	var items []partyItem
	for rows.Next() {
		var p partyItem
		_ = rows.Scan(&p.Code, &p.Name, &p.TaxID, &p.Telephone, &p.Address, &total)
		items = append(items, p)
	}
	if items == nil {
		items = []partyItem{}
	}

	maxPage = (total + size - 1) / size
	if maxPage < 1 {
		maxPage = 1
	}

	c.JSON(http.StatusOK, partyListResponse{
		Success: true,
		Data:    items,
		Pages: partyPages{
			Size: size, Page: page,
			TotalRecord: total, MaxPage: maxPage,
		},
	})
}

// GET /SMLJavaRESTService/v3/api/customer/:code
// GET /SMLJavaRESTService/v3/api/supplier/:code

type partyDetailResponse struct {
	Success bool      `json:"success"`
	Data    partyItem `json:"data"`
}

func (h *ReadHandler) GetCustomer(c *gin.Context) {
	h.getParty(c, "customer")
}

func (h *ReadHandler) GetSupplier(c *gin.Context) {
	h.getParty(c, "supplier")
}

func (h *ReadHandler) getParty(c *gin.Context, kind string) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	var table, detailTable, joinCol string
	if kind == "customer" {
		table, detailTable, joinCol = "ar_customer", "ar_customer_detail", "ar_code"
	} else {
		table, detailTable, joinCol = "ap_supplier", "ap_supplier_detail", "ap_code"
	}

	var p partyItem
	err := pool.QueryRow(ctx, `
		SELECT t.code, t.name_1, COALESCE(d.tax_id,''), COALESCE(t.telephone,''), COALESCE(t.address,'')
		FROM `+table+` t
		LEFT JOIN `+detailTable+` d ON t.code = d.`+joinCol+`
		WHERE t.code = $1`, code).
		Scan(&p.Code, &p.Name, &p.TaxID, &p.Telephone, &p.Address)
	if err != nil {
		c.JSON(http.StatusNotFound, partyDetailResponse{Success: false})
		return
	}
	c.JSON(http.StatusOK, partyDetailResponse{Success: true, Data: p})
}

// ─── Product ──────────────────────────────────────────────────────────────────
// GET /SMLJavaRESTService/product/v4?page=0&size=200
// GET /SMLJavaRESTService/v3/api/product/:code

type productV4Item struct {
	Code                  string            `json:"code"`
	Name                  string            `json:"name_1"`
	Name2                 string            `json:"name_2"`
	UnitStandard          string            `json:"unit_standard"`
	GroupMain             string            `json:"group_main"`
	BalanceQty            float64           `json:"balance_qty"`
	InventoryBarcode      []barcodeItem     `json:"inventory_barcode"`
	InventoryPriceFormula []priceFormula    `json:"inventory_price_formula"`
}

type barcodeItem struct {
	Price  float64 `json:"price"`
	Price0 string  `json:"price_0"`
}

type priceFormula struct {
	Price0 string `json:"price_0"`
}

type productV4Pages struct {
	Size        int `json:"size"`
	Page        int `json:"page"`
	TotalRecord int `json:"total_record"`
	MaxPage     int `json:"max_page"`
}

func (h *ReadHandler) ListProducts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "0"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "200"))
	if page < 0 {
		page = 0
	}
	if size < 1 || size > 500 {
		size = 200
	}
	offset := page * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	// Single query: COUNT(*) OVER() avoids a separate count round-trip.
	// LEFT JOIN on price_formula is filtered to sale_type=0 (retail price).
	// ic_inventory_pk_code index makes ORDER BY code efficient.
	rows, err := pool.Query(ctx, `
		SELECT i.code, COALESCE(i.name_1,''), COALESCE(i.name_2,''),
		       COALESCE(i.unit_standard,''), COALESCE(i.group_main,''),
		       COALESCE(i.balance_qty,0), COALESCE(pf.price_0,''),
		       COUNT(*) OVER() AS total_count
		FROM ic_inventory i
		LEFT JOIN ic_inventory_price_formula pf
		  ON pf.ic_code = i.code AND pf.unit_code = i.unit_standard AND pf.sale_type = 0
		WHERE i.status = 0
		ORDER BY i.code
		LIMIT $1 OFFSET $2`, size, offset)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer rows.Close()

	var items []productV4Item
	var total int
	for rows.Next() {
		var it productV4Item
		var price0 string
		_ = rows.Scan(&it.Code, &it.Name, &it.Name2, &it.UnitStandard, &it.GroupMain, &it.BalanceQty, &price0, &total)
		if price0 != "" {
			it.InventoryPriceFormula = []priceFormula{{Price0: price0}}
		} else {
			it.InventoryPriceFormula = []priceFormula{}
		}
		it.InventoryBarcode = []barcodeItem{}
		items = append(items, it)
	}
	if items == nil {
		items = []productV4Item{}
	}

	maxPage := 0
	if total > 0 {
		maxPage = (total - 1) / size
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    items,
		"pages": productV4Pages{
			Size: size, Page: page,
			TotalRecord: total, MaxPage: maxPage,
		},
	})
}

type productSingleData struct {
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Name2        string  `json:"name_2"`
	UnitStandard string  `json:"unit_standard"`
	GroupMain    string  `json:"group_main"`
	BalanceQty   float64 `json:"balance_qty"`
	Units        []struct {
		UnitCode string `json:"unit_code"`
	} `json:"units"`
}

func (h *ReadHandler) GetProduct(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	var p productSingleData
	err := pool.QueryRow(ctx, `
		SELECT code, COALESCE(name_1,''), COALESCE(name_2,''),
		       COALESCE(unit_standard,''), COALESCE(group_main,''), COALESCE(balance_qty,0)
		FROM ic_inventory WHERE code=$1 AND status=0`, code).
		Scan(&p.Code, &p.Name, &p.Name2, &p.UnitStandard, &p.GroupMain, &p.BalanceQty)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "product not found", "data": nil})
		return
	}
	if p.UnitStandard != "" {
		p.Units = []struct {
			UnitCode string `json:"unit_code"`
		}{{UnitCode: p.UnitStandard}}
	} else {
		p.Units = []struct {
			UnitCode string `json:"unit_code"`
		}{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": p})
}

// ─── Warehouse GET ────────────────────────────────────────────────────────────
// GET /SMLJavaRESTService/warehouse/v4?page=1&size=200
// GET /SMLJavaRESTService/warehouse/v4/:code

type shelfItem struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type warehouseItem struct {
	Code   string      `json:"code"`
	Name   string      `json:"name"`
	Shelves []shelfItem `json:"shelves"`
}

func (h *ReadHandler) ListWarehouses(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	rows, err := pool.Query(ctx, `
		SELECT code, COALESCE(name_1,'') FROM ic_warehouse WHERE status=0 ORDER BY code`)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer rows.Close()

	var warehouses []warehouseItem
	for rows.Next() {
		var w warehouseItem
		_ = rows.Scan(&w.Code, &w.Name)
		w.Shelves = []shelfItem{}
		warehouses = append(warehouses, w)
	}
	if warehouses == nil {
		warehouses = []warehouseItem{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": warehouses})
}

func (h *ReadHandler) GetWarehouse(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	var w warehouseItem
	err := pool.QueryRow(ctx, `
		SELECT code, COALESCE(name_1,'') FROM ic_warehouse WHERE code=$1`, code).
		Scan(&w.Code, &w.Name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "warehouse not found"})
		return
	}

	rows, err := pool.Query(ctx, `
		SELECT code, COALESCE(name_1,'') FROM ic_shelf WHERE whcode=$1 AND status=0 ORDER BY code`, code)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s shelfItem
			_ = rows.Scan(&s.Code, &s.Name)
			w.Shelves = append(w.Shelves, s)
		}
	}
	if w.Shelves == nil {
		w.Shelves = []shelfItem{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": w})
}
