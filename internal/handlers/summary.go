package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

type SummaryHandler struct {
	dbm *db.Manager
}

func NewSummaryHandler(dbm *db.Manager) *SummaryHandler {
	return &SummaryHandler{dbm: dbm}
}

type dailySummaryRow struct {
	DocDate     string  `json:"doc_date"`
	TransFlag   int     `json:"trans_flag"`
	DocCount    int     `json:"doc_count"`
	TotalValue  float64 `json:"total_value"`
	TotalVat    float64 `json:"total_vat"`
	TotalAmount float64 `json:"total_amount"`
}

// GET /api/v1/ic/transactions/summary
// Query params: date_from, date_to, trans_flag (optional)
// Uses nsi_ic_trans_sales_date_doc_idx (trans_flag, doc_date DESC, doc_no DESC).
func (h *SummaryHandler) DailySummary(c *gin.Context) {
	dateFrom := c.DefaultQuery("date_from", time.Now().Format("2006-01-02"))
	dateTo := c.DefaultQuery("date_to", dateFrom)
	transFlag := c.Query("trans_flag")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	where := "WHERE last_status = 0 AND doc_date BETWEEN $1 AND $2"
	args := []interface{}{dateFrom, dateTo}
	if transFlag != "" {
		where += " AND trans_flag = $3"
		args = append(args, transFlag)
	}

	rows, err := pool.Query(ctx, `
		SELECT doc_date::text,
		       trans_flag,
		       COUNT(*) AS doc_count,
		       COALESCE(SUM(total_value), 0),
		       COALESCE(SUM(total_vat_value), 0),
		       COALESCE(SUM(total_amount), 0)
		FROM ic_trans
		`+where+`
		GROUP BY doc_date, trans_flag
		ORDER BY doc_date DESC, trans_flag`, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var result []dailySummaryRow
	for rows.Next() {
		var r dailySummaryRow
		if err := rows.Scan(&r.DocDate, &r.TransFlag, &r.DocCount,
			&r.TotalValue, &r.TotalVat, &r.TotalAmount); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		result = append(result, r)
	}
	if result == nil {
		result = []dailySummaryRow{}
	}

	c.JSON(http.StatusOK, gin.H{
		"date_from": dateFrom,
		"date_to":   dateTo,
		"data":      result,
	})
}
