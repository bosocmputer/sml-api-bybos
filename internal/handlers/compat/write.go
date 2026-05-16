package compat

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/models"
)

type WriteHandler struct {
	dbm *db.Manager
}

func NewWriteHandler(dbm *db.Manager) *WriteHandler {
	return &WriteHandler{dbm: dbm}
}

// ─── Shared item shape (saleorder / saleinvoice / purchaseorder ใช้ร่วมกัน) ──

type compatItem struct {
	ItemCode  string  `json:"item_code"`
	ItemName  string  `json:"item_name"`
	UnitCode  string  `json:"unit_code"`
	WHCode    string  `json:"wh_code"`
	ShelfCode string  `json:"shelf_code"`
	Qty       float64 `json:"qty"`
	Price     float64 `json:"price"`
	SumAmount float64 `json:"sum_amount"`
}

// ─── SaleOrder POST /SMLJavaRESTService/v3/api/saleorder ─────────────────────

type saleOrderPayload struct {
	DocNo      string       `json:"doc_no"`
	DocDate    string       `json:"doc_date"`
	DocTime    string       `json:"doc_time"`
	CustCode   string       `json:"cust_code"`
	SaleCode   string       `json:"sale_code"`
	BranchCode string       `json:"branch_code"`
	VATType    int          `json:"vat_type"`
	VATRate    float64      `json:"vat_rate"`
	TotalAmount float64     `json:"total_amount"`
	Remark     string       `json:"remark"`
	Items      []compatItem `json:"items"`
}

func (h *WriteHandler) CreateSaleOrder(c *gin.Context) {
	var p saleOrderPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		errV3(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.insertDoc(c, p.DocNo, p.DocDate, p.DocTime, models.TransFlagSaleOrder,
		models.TransTypeSale, p.CustCode, p.SaleCode, p.BranchCode,
		p.VATType, p.VATRate, p.Remark, p.Items); err != nil {
		return
	}
	okV3(c, gin.H{"doc_no": p.DocNo})
}

// ─── SaleInvoice POST /SMLJavaRESTService/saleinvoice/v4 ─────────────────────

type saleInvoicePayload struct {
	DocNo      string       `json:"doc_no"`
	DocDate    string       `json:"doc_date"`
	DocTime    string       `json:"doc_time"`
	CustCode   string       `json:"cust_code"`
	SaleCode   string       `json:"sale_code"`
	BranchCode string       `json:"branch_code"`
	VATType    int          `json:"vat_type"`
	VATRate    float64      `json:"vat_rate"`
	TotalAmount float64     `json:"total_amount"`
	Remark     string       `json:"remark"`
	Details    []compatItem `json:"details"` // saleinvoice ใช้ "details" ไม่ใช่ "items"
}

func (h *WriteHandler) CreateSaleInvoice(c *gin.Context) {
	var p saleInvoicePayload
	if err := c.ShouldBindJSON(&p); err != nil {
		errV3(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.insertDoc(c, p.DocNo, p.DocDate, p.DocTime, models.TransFlagSaleInvoice,
		models.TransTypeSale, p.CustCode, p.SaleCode, p.BranchCode,
		p.VATType, p.VATRate, p.Remark, p.Details); err != nil {
		return
	}
	okV3(c, gin.H{"doc_no": p.DocNo})
}

// ─── PurchaseOrder POST /SMLJavaRESTService/v3/api/purchaseorder ─────────────

type purchaseOrderPayload struct {
	DocNo      string       `json:"doc_no"`
	DocDate    string       `json:"doc_date"`
	DocTime    string       `json:"doc_time"`
	CustCode   string       `json:"cust_code"` // supplier code on a PO
	SaleCode   string       `json:"sale_code"`
	BranchCode string       `json:"branch_code"`
	WHCode     string       `json:"wh_code"`
	ShelfCode  string       `json:"shelf_code"`
	VATType    int          `json:"vat_type"`
	VATRate    float64      `json:"vat_rate"`
	TotalAmount float64     `json:"total_amount"`
	Remark     string       `json:"remark"`
	Items      []compatItem `json:"items"`
}

func (h *WriteHandler) CreatePurchaseOrder(c *gin.Context) {
	var p purchaseOrderPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		errV3(c, http.StatusBadRequest, err.Error())
		return
	}
	// fallback wh/shelf จาก header → item level
	for i := range p.Items {
		if p.Items[i].WHCode == "" {
			p.Items[i].WHCode = p.WHCode
		}
		if p.Items[i].ShelfCode == "" {
			p.Items[i].ShelfCode = p.ShelfCode
		}
	}
	if err := h.insertDoc(c, p.DocNo, p.DocDate, p.DocTime, models.TransFlagPurchaseOrder,
		models.TransTypePurchase, p.CustCode, p.SaleCode, p.BranchCode,
		p.VATType, p.VATRate, p.Remark, p.Items); err != nil {
		return
	}
	okV3(c, gin.H{"doc_no": p.DocNo})
}

// ─── Product POST /SMLJavaRESTService/v3/api/product ─────────────────────────

type createProductUnit struct {
	UnitCode    string  `json:"unit_code"`
	UnitName    string  `json:"unit_name"`
	StandValue  float64 `json:"stand_value"`
	DivideValue float64 `json:"divide_value"`
}

type createProductPriceFormula struct {
	UnitCode      string `json:"unit_code"`
	SaleType      int    `json:"sale_type"`
	Price0        string `json:"price_0"`
	TaxType       int    `json:"tax_type"`
	PriceCurrency int    `json:"price_currency"`
}

type createProductPayload struct {
	Code          string                      `json:"code"`
	Name          string                      `json:"name"`
	NameEng       string                      `json:"name_eng"`
	NameEng2      string                      `json:"name_eng_2"`
	TaxType       int                         `json:"tax_type"`
	ItemType      int                         `json:"item_type"`
	UnitType      int                         `json:"unit_type"`
	UnitCost      string                      `json:"unit_cost"`
	UnitStandard  string                      `json:"unit_standard"`
	ItemCategory  string                      `json:"item_category"`
	CategoryName  string                      `json:"category_name"`
	GroupMain     string                      `json:"group_main"`
	GroupMainName string                      `json:"group_main_name"`
	GroupSub      string                      `json:"group_sub"`
	PurchasePoint int                         `json:"purchase_point"`
	Units         []createProductUnit         `json:"units"`
	PriceFormulas []createProductPriceFormula `json:"price_formulas"`
}

func (h *WriteHandler) CreateProduct(c *gin.Context) {
	var p createProductPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		errV3(c, http.StatusBadRequest, err.Error())
		return
	}
	if p.Code == "" || p.Name == "" {
		errV3(c, http.StatusBadRequest, "code and name are required")
		return
	}
	unit := p.UnitStandard
	if unit == "" {
		unit = p.UnitCost
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		errV3(c, http.StatusInternalServerError, fmt.Sprintf("begin tx: %v", err))
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO ic_inventory (
			code, name_1, name_eng_1, name_eng_2,
			tax_type, item_type, unit_type,
			unit_cost, unit_standard, group_main, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0)`,
		p.Code, p.Name, p.NameEng, p.NameEng2,
		p.TaxType, p.ItemType, p.UnitType,
		p.UnitCost, unit, p.GroupMain,
	)
	if err != nil {
		if strContains(err.Error(), "duplicate key") || strContains(err.Error(), "unique constraint") {
			errV3(c, http.StatusConflict, fmt.Sprintf("product code '%s' already exists", p.Code))
			return
		}
		errV3(c, http.StatusInternalServerError, fmt.Sprintf("insert product: %v", err))
		return
	}

	for i, pf := range p.PriceFormulas {
		_, err = tx.Exec(ctx, `
			INSERT INTO ic_inventory_price_formula (ic_code, unit_code, sale_type, price_0, tax_type)
			VALUES ($1,$2,$3,$4,$5)`,
			p.Code, pf.UnitCode, pf.SaleType, pf.Price0, pf.TaxType,
		)
		if err != nil {
			errV3(c, http.StatusInternalServerError, fmt.Sprintf("insert price_formula %d: %v", i, err))
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		errV3(c, http.StatusInternalServerError, fmt.Sprintf("commit: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "product created",
		"data":    gin.H{"code": p.Code},
	})
}

// ─── insertDoc — shared write logic ──────────────────────────────────────────

func (h *WriteHandler) insertDoc(
	c *gin.Context,
	docNo, docDateStr, docTime string,
	transFlag, transType int,
	custCode, saleCode, branchCode string,
	vatType int, vatRate float64,
	remark string,
	items []compatItem,
) error {
	docDate, err := parseDate(docDateStr)
	if err != nil {
		errV3(c, http.StatusBadRequest, "doc_date format must be YYYY-MM-DD")
		return err
	}
	if docTime == "" {
		docTime = "09:00"
	}
	if branchCode == "" {
		branchCode = "001"
	}

	// คำนวณยอด
	var totalValue float64
	for _, it := range items {
		totalValue += it.Qty * it.Price
	}
	var totalVat, totalAmount float64
	switch vatType {
	case 0:
		totalVat = round2(totalValue * vatRate / 100)
		totalAmount = totalValue + totalVat
	case 1:
		totalAmount = totalValue
		totalVat = round2(totalValue * vatRate / (100 + vatRate))
		totalValue = round2(totalAmount - totalVat)
	default:
		totalAmount = totalValue
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool := getPool(c, h.dbm)
	if pool == nil {
		return fmt.Errorf("no pool")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		errV3(c, http.StatusInternalServerError, fmt.Sprintf("begin tx: %v", err))
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO ic_trans (
			trans_type, trans_flag, doc_date, doc_no,
			cust_code, branch_code, sale_code,
			vat_type, vat_rate,
			total_value, total_vat_value, total_after_vat, total_amount,
			remark, doc_time, last_status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,0)`,
		transType, transFlag, docDate, docNo,
		custCode, branchCode, saleCode,
		vatType, vatRate,
		totalValue, totalVat, totalAmount, totalAmount,
		remark, docTime,
	)
	if err != nil {
		if strContains(err.Error(), "duplicate key") || strContains(err.Error(), "unique constraint") {
			errV3(c, http.StatusConflict, fmt.Sprintf("doc_no '%s' already exists", docNo))
			return err
		}
		errV3(c, http.StatusInternalServerError, fmt.Sprintf("insert header: %v", err))
		return err
	}

	for i, it := range items {
		sumAmount := it.Qty * it.Price
		_, err = tx.Exec(ctx, `
			INSERT INTO ic_trans_detail (
				trans_type, trans_flag, doc_date, doc_no,
				item_code, item_name, unit_code,
				qty, price, sum_amount,
				wh_code, shelf_code,
				cust_code, line_number, last_status
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0)`,
			transType, transFlag, docDate, docNo,
			it.ItemCode, it.ItemName, it.UnitCode,
			it.Qty, it.Price, sumAmount,
			it.WHCode, it.ShelfCode,
			custCode, i,
		)
		if err != nil {
			errV3(c, http.StatusInternalServerError, fmt.Sprintf("insert item %d: %v", i, err))
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		errV3(c, http.StatusInternalServerError, fmt.Sprintf("commit: %v", err))
		return err
	}
	return nil
}
