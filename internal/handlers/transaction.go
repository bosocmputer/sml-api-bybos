package handlers

import (
	"context"
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
