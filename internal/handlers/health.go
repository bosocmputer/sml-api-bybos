package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
)

type HealthHandler struct {
	dbm *db.Manager
}

func NewHealthHandler(dbm *db.Manager) *HealthHandler {
	return &HealthHandler{dbm: dbm}
}

func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	// ทดสอบ ping DB ด้วย tenant จาก header (ถ้ามี) หรือ sml1 เป็น default
	dbName := c.GetHeader("X-Tenant")
	if dbName == "" {
		dbName = "sml1"
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
	c.JSON(http.StatusOK, gin.H{"status": "ok", "database": dbName})
}
