package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

type DocFormatHandler struct {
	dbm *db.Manager
}

func NewDocFormatHandler(dbm *db.Manager) *DocFormatHandler {
	return &DocFormatHandler{dbm: dbm}
}

type DocFormatItem struct {
	Code       string `json:"code"`
	Name1      string `json:"name_1"`
	Name2      string `json:"name_2"`
	Format     string `json:"format"`
	ScreenCode string `json:"screen_code"`
}

// GET /api/v1/ic/doc-formats?screen_code=PO
// screen_code: PO=ใบสั่งซื้อ, SI=ขายสินค้าและบริการ, SR=ใบสั่งขาย
func (h *DocFormatHandler) List(c *gin.Context) {
	screenCode := strings.ToUpper(strings.TrimSpace(c.Query("screen_code")))

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	query := `SELECT code, COALESCE(name_1,''), COALESCE(name_2,''), COALESCE(format,''), COALESCE(screen_code,'')
	          FROM erp_doc_format`
	args := []any{}
	if screenCode != "" {
		query += " WHERE screen_code = $1"
		args = append(args, screenCode)
	}
	query += " ORDER BY code"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer rows.Close()

	var items []DocFormatItem
	for rows.Next() {
		var f DocFormatItem
		if err := rows.Scan(&f.Code, &f.Name1, &f.Name2, &f.Format, &f.ScreenCode); err != nil {
			continue
		}
		items = append(items, f)
	}
	if items == nil {
		items = []DocFormatItem{}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}
