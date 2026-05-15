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

type CustomerHandler struct {
	dbm *db.Manager
}

func NewCustomerHandler(dbm *db.Manager) *CustomerHandler {
	return &CustomerHandler{dbm: dbm}
}

func (h *CustomerHandler) List(c *gin.Context) {
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

	baseWhere := "WHERE c.status = 0"
	args := pgx.NamedArgs{}
	if search != "" {
		baseWhere += " AND (c.code ILIKE @search OR c.name_1 ILIKE @search OR c.telephone ILIKE @search)"
		args["search"] = "%" + search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ar_customer c "+baseWhere, args).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	query := `SELECT c.code, c.name_1, COALESCE(c.name_2,''), COALESCE(c.telephone,''),
		COALESCE(c.email,''), COALESCE(c.address,''), COALESCE(d.tax_id,''), c.status
		FROM ar_customer c
		LEFT JOIN ar_customer_detail d ON c.code = d.ar_code
		` + baseWhere + ` ORDER BY c.code LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var customers []models.Customer
	for rows.Next() {
		var cu models.Customer
		if err := rows.Scan(&cu.Code, &cu.Name1, &cu.Name2, &cu.Telephone,
			&cu.Email, &cu.Address, &cu.TaxID, &cu.Status); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		customers = append(customers, cu)
	}
	if customers == nil {
		customers = []models.Customer{}
	}

	c.JSON(http.StatusOK, models.CustomerListResponse{
		Data: customers, Total: total, Page: page, Size: size,
	})
}

func (h *CustomerHandler) Get(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var cu models.Customer
	err = pool.QueryRow(ctx,
		`SELECT c.code, c.name_1, COALESCE(c.name_2,''), COALESCE(c.telephone,''),
		COALESCE(c.email,''), COALESCE(c.address,''), COALESCE(d.tax_id,''), c.status
		FROM ar_customer c
		LEFT JOIN ar_customer_detail d ON c.code = d.ar_code
		WHERE c.code = $1`, code).
		Scan(&cu.Code, &cu.Name1, &cu.Name2, &cu.Telephone, &cu.Email, &cu.Address, &cu.TaxID, &cu.Status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "customer not found"})
		return
	}
	c.JSON(http.StatusOK, cu)
}
