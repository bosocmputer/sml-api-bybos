package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type TransactionHandler struct {
	dbm *db.Manager
}

func NewTransactionHandler(dbm *db.Manager) *TransactionHandler {
	return &TransactionHandler{dbm: dbm}
}

func (h *TransactionHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	args := pgx.NamedArgs{}
	conditions := "WHERE last_status = 0"

	if tf := c.Query("trans_flag"); tf != "" {
		conditions += " AND trans_flag = @trans_flag"
		args["trans_flag"] = tf
	}
	if df := c.Query("date_from"); df != "" {
		conditions += " AND doc_date >= @date_from"
		args["date_from"] = df
	}
	if dt := c.Query("date_to"); dt != "" {
		conditions += " AND doc_date <= @date_to"
		args["date_to"] = dt
	}
	if cc := c.Query("cust_code"); cc != "" {
		conditions += " AND cust_code = @cust_code"
		args["cust_code"] = cc
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ic_trans "+conditions, args).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	query := `SELECT doc_no, doc_date, COALESCE(doc_group,''), trans_flag,
		COALESCE(cust_code,''), vat_type, COALESCE(vat_rate,0),
		COALESCE(total_value,0), COALESCE(total_vat_value,0), COALESCE(total_amount,0),
		COALESCE(remark,''), status, COALESCE(sale_code,''), COALESCE(branch_code,'')
		FROM ic_trans ` + conditions + `
		ORDER BY doc_date DESC, doc_no DESC
		LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var txns []models.Transaction
	for rows.Next() {
		var t models.Transaction
		var docDate time.Time
		if err := rows.Scan(&t.DocNo, &docDate, &t.DocGroup, &t.TransFlag,
			&t.CustCode, &t.VatType, &t.VatRate,
			&t.TotalValue, &t.TotalVat, &t.TotalAmount,
			&t.Remark, &t.Status, &t.SaleCode, &t.BranchCode); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		t.DocDate = docDate.Format("2006-01-02")
		txns = append(txns, t)
	}
	if txns == nil {
		txns = []models.Transaction{}
	}

	c.JSON(http.StatusOK, models.TransactionListResponse{
		Data: txns, Total: total, Page: page, Size: size,
	})
}

func (h *TransactionHandler) Get(c *gin.Context) {
	docNo := c.Param("doc_no")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var t models.Transaction
	var docDate time.Time
	err = pool.QueryRow(ctx,
		`SELECT doc_no, doc_date, COALESCE(doc_group,''), trans_flag,
		COALESCE(cust_code,''), vat_type, COALESCE(vat_rate,0),
		COALESCE(total_value,0), COALESCE(total_vat_value,0), COALESCE(total_amount,0),
		COALESCE(remark,''), status, COALESCE(sale_code,''), COALESCE(branch_code,'')
		FROM ic_trans WHERE doc_no = $1 AND last_status = 0`, docNo).
		Scan(&t.DocNo, &docDate, &t.DocGroup, &t.TransFlag,
			&t.CustCode, &t.VatType, &t.VatRate,
			&t.TotalValue, &t.TotalVat, &t.TotalAmount,
			&t.Remark, &t.Status, &t.SaleCode, &t.BranchCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
		return
	}
	t.DocDate = docDate.Format("2006-01-02")

	// ดึง items
	rows, err := pool.Query(ctx,
		`SELECT COALESCE(item_code,''), COALESCE(item_name,''), COALESCE(unit_code,''),
		COALESCE(qty,0), COALESCE(price,0), COALESCE(sum_amount,0),
		COALESCE(wh_code,''), COALESCE(shelf_code,''), line_number
		FROM ic_trans_detail
		WHERE doc_no = $1 AND last_status = 0
		ORDER BY line_number`, docNo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var item models.TransactionItem
		if err := rows.Scan(&item.ItemCode, &item.ItemName, &item.UnitCode,
			&item.Qty, &item.Price, &item.SumAmount,
			&item.WHCode, &item.ShelfCode, &item.LineNumber); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		t.Items = append(t.Items, item)
	}
	if t.Items == nil {
		t.Items = []models.TransactionItem{}
	}

	c.JSON(http.StatusOK, t)
}

// Create — POST /api/v1/ic/transactions
// สร้าง document ใหม่ใน ic_trans + ic_trans_detail ภายใน transaction เดียว
func (h *TransactionHandler) Create(c *gin.Context) {
	var req models.CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	docDate, err := time.Parse("2006-01-02", req.DocDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "doc_date format must be YYYY-MM-DD"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// คำนวณ VAT และยอดรวม
	transType := models.TransTypeOf(req.TransFlag)
	totalValue, totalVat, totalAmount := calcVat(req.Items, req.VatType, req.VatRate)

	docTime := req.DocTime
	if docTime == "" {
		docTime = "09:00"
	}
	branchCode := req.BranchCode
	if branchCode == "" {
		branchCode = "001"
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("begin tx: %v", err)})
		return
	}
	defer tx.Rollback(ctx)

	// Insert header
	_, err = tx.Exec(ctx, `
		INSERT INTO ic_trans (
			trans_type, trans_flag, doc_date, doc_no,
			cust_code, branch_code, sale_code,
			vat_type, vat_rate,
			total_value, total_vat_value, total_after_vat, total_amount,
			remark, doc_time, last_status
		) VALUES (
			$1,$2,$3,$4,
			$5,$6,$7,
			$8,$9,
			$10,$11,$12,$13,
			$14,$15,0
		)`,
		transType, req.TransFlag, docDate, req.DocNo,
		req.CustCode, branchCode, req.SaleCode,
		req.VatType, req.VatRate,
		totalValue, totalVat, totalAmount, totalAmount,
		req.Remark, docTime,
	)
	if err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("doc_no '%s' already exists for trans_flag %d", req.DocNo, req.TransFlag)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("insert header: %v", err)})
		return
	}

	// Insert detail lines
	for i, item := range req.Items {
		whCode := item.WHCode
		if whCode == "" {
			whCode = req.WHCode
		}
		shelfCode := item.ShelfCode
		if shelfCode == "" {
			shelfCode = req.ShelfCode
		}
		sumAmount := item.Qty * item.Price

		_, err = tx.Exec(ctx, `
			INSERT INTO ic_trans_detail (
				trans_type, trans_flag, doc_date, doc_no,
				item_code, item_name, unit_code,
				qty, price, sum_amount,
				wh_code, shelf_code,
				cust_code, line_number, last_status
			) VALUES (
				$1,$2,$3,$4,
				$5,$6,$7,
				$8,$9,$10,
				$11,$12,
				$13,$14,0
			)`,
			transType, req.TransFlag, docDate, req.DocNo,
			item.ItemCode, item.ItemName, item.UnitCode,
			item.Qty, item.Price, sumAmount,
			whCode, shelfCode,
			req.CustCode, i,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("insert item line %d: %v", i, err)})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("commit: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"doc_no":       req.DocNo,
		"trans_flag":   req.TransFlag,
		"total_value":  totalValue,
		"total_vat":    totalVat,
		"total_amount": totalAmount,
		"items":        len(req.Items),
	})
}

func calcVat(items []models.CreateTransactionItemReq, vatType int, vatRate float64) (totalValue, totalVat, totalAmount float64) {
	for _, it := range items {
		totalValue += it.Qty * it.Price
	}
	switch vatType {
	case 0: // แยกนอก — VAT คำนวณบนยอดก่อน VAT
		totalVat = round2(totalValue * vatRate / 100)
		totalAmount = totalValue + totalVat
	case 1: // รวมใน — ยอดรวม VAT อยู่แล้ว
		totalAmount = totalValue
		totalVat = round2(totalValue * vatRate / (100 + vatRate))
		totalValue = totalAmount - totalVat
	case 2: // ศูนย์%
		totalAmount = totalValue
	}
	return
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func isUniqueViolation(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
