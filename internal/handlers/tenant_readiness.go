package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/config"
	"sml-api-bybos/internal/smltenant"
)

type TenantReadinessHandler struct {
	cfg *config.Config
}

func NewTenantReadinessHandler(cfg *config.Config) *TenantReadinessHandler {
	return &TenantReadinessHandler{cfg: cfg}
}

func (h *TenantReadinessHandler) Get(c *gin.Context) {
	tenant := smltenant.NormalizeTenant(c.Query("tenant"))
	if tenant == "" {
		api.BadRequest(c, "tenant_required", "tenant is required", nil)
		return
	}
	template := smltenant.NormalizeTenant(c.Query("template"))
	if template == "" {
		template = h.cfg.Auth.ImageTemplateDatabase
	}
	if template == "" {
		api.Internal(c, "tenant_readiness_template_missing", "SML image template database is not configured", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	report, err := smltenant.VerifyTenant(ctx, h.cfg, smltenant.VerifyOptions{
		Tenant:        tenant,
		Template:      template,
		AdminDatabase: "postgres",
	})
	if err != nil {
		api.Error(c, http.StatusBadGateway, "tenant_readiness_failed", "tenant readiness check failed", gin.H{"tenant": tenant})
		return
	}
	api.OK(c, tenantReadinessFromReport(report))
}

func tenantReadinessFromReport(report smltenant.VerifyReport) gin.H {
	status := "ready"
	message := "tenant is ready"
	if !report.OK {
		status = "schema_mismatch"
		message = "tenant image schema is not ready"
		for _, check := range report.Checks {
			if check.Status == smltenant.CheckOK {
				continue
			}
			switch check.Name {
			case "main_database":
				status = "main_db_missing"
				message = check.Message
			case "image_database":
				status = "image_db_missing"
				message = check.Message
			default:
				if strings.Contains(check.Name, "columns") {
					status = "schema_mismatch"
					message = check.Message
				}
			}
			break
		}
	}
	return gin.H{
		"ok":            report.OK,
		"status":        status,
		"message":       message,
		"tenant":        report.Tenant,
		"imageDatabase": report.ImageDatabase,
		"template":      report.Template,
		"checks":        report.Checks,
	}
}
