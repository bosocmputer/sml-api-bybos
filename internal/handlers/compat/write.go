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
