package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const TenantKey = "tenant_db"

// Tenant reads X-Tenant header (DB name) and stores it in context.
func Tenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant := c.GetHeader("X-Tenant")
		if tenant == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Tenant header required (database name)"})
			return
		}
		c.Set(TenantKey, tenant)
		c.Next()
	}
}
