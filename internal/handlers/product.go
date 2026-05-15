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

type ProductHandler struct {
	dbm *db.Manager
}

func NewProductHandler(dbm *db.Manager) *ProductHandler {
	return &ProductHandler{dbm: dbm}
}

func (h *ProductHandler) List(c *gin.Context) {
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

	baseWhere := "WHERE status = 0"
	args := pgx.NamedArgs{}
	if search != "" {
		baseWhere += " AND (code ILIKE @search OR name_1 ILIKE @search)"
		args["search"] = "%" + search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ic_inventory "+baseWhere, args).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	query := `SELECT code, name_1, COALESCE(name_2,''), unit_standard, COALESCE(unit_standard_name,''),
		COALESCE(balance_qty,0), COALESCE(average_cost,0), item_status, status
		FROM ic_inventory ` + baseWhere + ` ORDER BY code LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.Code, &p.Name1, &p.Name2, &p.UnitStd, &p.UnitStdName,
			&p.BalanceQty, &p.AverageCost, &p.ItemStatus, &p.Status); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		products = append(products, p)
	}
	if products == nil {
		products = []models.Product{}
	}

	c.JSON(http.StatusOK, models.ProductListResponse{
		Data: products, Total: total, Page: page, Size: size,
	})
}

func (h *ProductHandler) Get(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var p models.Product
	err = pool.QueryRow(ctx,
		`SELECT code, name_1, COALESCE(name_2,''), unit_standard, COALESCE(unit_standard_name,''),
		COALESCE(balance_qty,0), COALESCE(average_cost,0), item_status, status
		FROM ic_inventory WHERE code = $1`, code).
		Scan(&p.Code, &p.Name1, &p.Name2, &p.UnitStd, &p.UnitStdName,
			&p.BalanceQty, &p.AverageCost, &p.ItemStatus, &p.Status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}
