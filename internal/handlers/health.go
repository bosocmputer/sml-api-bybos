package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/config"
	"sml-api-bybos/internal/db"
)

type HealthHandler struct {
	dbm *db.Manager
	cfg *config.Config
}

func NewHealthHandler(dbm *db.Manager, cfg *config.Config) *HealthHandler {
	return &HealthHandler{dbm: dbm, cfg: cfg}
}

func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	// Readiness checks the configured/default tenant and touches core SML
	// tables, so "ready" means BillFlow can actually use the API.
	dbName := readyTenantName(c, h.cfg.DB.DefaultTenant)
	if dbName == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "detail": "no tenant configured"})
		return
	}

	pool, err := h.dbm.Get(ctx, dbName)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "detail": err.Error()})
		return
	}
	if err := pool.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "detail": err.Error()})
		return
	}
	var ok bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ic_inventory LIMIT 1)`).Scan(&ok); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "database": dbName, "detail": "ic_inventory check failed: " + err.Error()})
		return
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ic_trans LIMIT 1)`).Scan(&ok); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "database": dbName, "detail": "ic_trans check failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "database": dbName})
}

func readyTenantName(c *gin.Context, defaultTenant string) string {
	dbName := c.GetHeader("X-Tenant")
	if dbName == "" {
		dbName = c.GetHeader("databaseName")
	}
	if dbName == "" {
		dbName = defaultTenant
	}
	return strings.ToLower(strings.TrimSpace(dbName))
}
