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

type SupplierHandler struct {
	dbm *db.Manager
}

func NewSupplierHandler(dbm *db.Manager) *SupplierHandler {
	return &SupplierHandler{dbm: dbm}
}

func (h *SupplierHandler) List(c *gin.Context) {
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	baseWhere := "WHERE s.status = 0"
	args := pgx.NamedArgs{}
	if search != "" {
		baseWhere += " AND (s.code ILIKE @search OR s.name_1 ILIKE @search OR s.telephone ILIKE @search)"
		args["search"] = "%" + search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ap_supplier s "+baseWhere, args).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	query := `SELECT s.code, s.name_1, COALESCE(s.name_2,''), COALESCE(s.telephone,''),
		COALESCE(s.email,''), COALESCE(s.address,''), COALESCE(d.tax_id,''), s.status
		FROM ap_supplier s
		LEFT JOIN ap_supplier_detail d ON s.code = d.ap_code
		` + baseWhere + ` ORDER BY s.code LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var suppliers []models.Supplier
	for rows.Next() {
		var s models.Supplier
		if err := rows.Scan(&s.Code, &s.Name1, &s.Name2, &s.Telephone,
			&s.Email, &s.Address, &s.TaxID, &s.Status); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		suppliers = append(suppliers, s)
	}
	if suppliers == nil {
		suppliers = []models.Supplier{}
	}

	c.JSON(http.StatusOK, models.SupplierListResponse{
		Data: suppliers, Total: total, Page: page, Size: size,
	})
}

func (h *SupplierHandler) Get(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var s models.Supplier
	err = pool.QueryRow(ctx,
		`SELECT s.code, s.name_1, COALESCE(s.name_2,''), COALESCE(s.telephone,''),
		COALESCE(s.email,''), COALESCE(s.address,''), COALESCE(d.tax_id,''), s.status
		FROM ap_supplier s
		LEFT JOIN ap_supplier_detail d ON s.code = d.ap_code
		WHERE s.code = $1`, code).
		Scan(&s.Code, &s.Name1, &s.Name2, &s.Telephone, &s.Email, &s.Address, &s.TaxID, &s.Status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "supplier not found"})
		return
	}
	c.JSON(http.StatusOK, s)
}
