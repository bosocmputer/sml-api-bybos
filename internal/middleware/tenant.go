package middleware

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/api"
)

const TenantKey = "tenant_db"

var validDBName = regexp.MustCompile(`^[a-zA-Z0-9_]{1,63}$`)

// Tenant resolves the target DB name using this priority:
//  1. X-Tenant header  (new apps)
//  2. databaseName header (BillFlow / SML legacy clients)
//  3. defaultTenant from env DEFAULT_TENANT  (single-tenant deployments)
//
// The value is lowercased and validated against a safe identifier pattern
// before being stored in context. An optional allowedTenants set (non-empty)
// restricts which DB names are permitted.
func Tenant(defaultTenant string, allowedTenants map[string]struct{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant := c.GetHeader("X-Tenant")
		if tenant == "" {
			tenant = c.GetHeader("databaseName")
		}
		if tenant == "" {
			tenant = defaultTenant
		}
		if tenant == "" {
			api.BadRequest(c, "tenant_missing", "tenant not specified (use X-Tenant or databaseName header)", nil)
			c.Abort()
			return
		}

		// Normalize: SML Java sends uppercase names like "SML1_2026"
		tenant = strings.ToLower(tenant)

		if !validDBName.MatchString(tenant) {
			api.BadRequest(c, "tenant_invalid", "tenant name contains invalid characters", nil)
			c.Abort()
			return
		}

		// Whitelist check (skip when allowedTenants is empty = allow all)
		if len(allowedTenants) > 0 {
			if _, ok := allowedTenants[tenant]; !ok {
				api.Forbidden(c, "tenant_not_allowed", "tenant not allowed: "+tenant, nil)
				c.Abort()
				return
			}
		}

		c.Set(TenantKey, tenant)
		c.Next()
	}
}
