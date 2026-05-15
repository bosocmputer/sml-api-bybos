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

	var total int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE status=0").Scan(&total)

	maxPage := (total + size - 1) / size
	if maxPage < 1 {
		maxPage = 1
	}

	rows, err := pool.Query(ctx, `
		SELECT t.code, t.name_1, COALESCE(d.tax_id,''), COALESCE(t.telephone,''), COALESCE(t.address,'')
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
		_ = rows.Scan(&p.Code, &p.Name, &p.TaxID, &p.Telephone, &p.Address)
		items = append(items, p)
	}
	if items == nil {
		items = []partyItem{}
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

// ─── Product GET ──────────────────────────────────────────────────────────────
// GET /SMLJavaRESTService/v3/api/product/:code

type productData struct {
	Code           string `json:"code"`
	UnitStandard   string `json:"unit_standard"`
	StartSaleUnit  string `json:"start_sale_unit"`
	StartSaleWH    string `json:"start_sale_wh"`
	StartSaleShelf string `json:"start_sale_shelf"`
}

func (h *ReadHandler) GetProduct(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	var p productData
	err := pool.QueryRow(ctx, `
		SELECT code, COALESCE(unit_standard,''), COALESCE(unit_standard,''), '', ''
		FROM ic_inventory WHERE code=$1`, code).
		Scan(&p.Code, &p.UnitStandard, &p.StartSaleUnit, &p.StartSaleWH, &p.StartSaleShelf)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "product not found"})
		return
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
