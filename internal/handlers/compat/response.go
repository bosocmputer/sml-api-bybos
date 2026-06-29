// Package compat implements SML REST API-compatible endpoints so BillFlow
// can point SHOPEE_SML_URL at sml-api-bybos with zero code changes.
// All paths and response shapes match SMLJavaRESTService3 exactly.
package compat

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

// v3Response is the standard success envelope BillFlow expects.
type v3Response struct {
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data"`
}

func okV3(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, v3Response{Status: "success", Data: data})
}

func errV3(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"status": "error", "message": msg})
}

// getPool resolves tenant pool; returns nil and writes error if unavailable.
func getPool(c *gin.Context, dbm *db.Manager) *pgxpool.Pool {
	ctx := c.Request.Context()
	pool, err := dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return nil
	}
	return pool
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

func strContains(s, sub string) bool { return strings.Contains(s, sub) }
