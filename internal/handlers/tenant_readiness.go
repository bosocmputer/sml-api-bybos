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

type provisionTenantImageDatabaseRequest struct {
	Tenant   string `json:"tenant"`
	Template string `json:"template"`
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

func (h *TenantReadinessHandler) ProvisionImageDatabase(c *gin.Context) {
	var req provisionTenantImageDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	tenant := smltenant.NormalizeTenant(req.Tenant)
	if tenant == "" {
		api.BadRequest(c, "tenant_required", "tenant is required", nil)
		return
	}
	if len(h.cfg.DB.AllowedTenants) > 0 {
		if _, ok := h.cfg.DB.AllowedTenants[tenant]; !ok {
			api.Forbidden(c, "tenant_not_allowed", "tenant is not allowed by this API", gin.H{"tenant": tenant})
			return
		}
	}
	template := smltenant.NormalizeTenant(req.Template)
	if template == "" {
		template = h.cfg.Auth.ImageTemplateDatabase
	}
	if template == "" {
		api.Internal(c, "tenant_readiness_template_missing", "SML image template database is not configured", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
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
	if report.OK {
		api.OK(c, gin.H{
			"provisioned": false,
			"readiness":   tenantReadinessFromReport(report),
		})
		return
	}
	if !tenantCanProvisionDocImages(report) {
		api.Error(c, http.StatusFailedDependency, "tenant_not_provisionable", "tenant cannot be provisioned automatically", tenantReadinessFromReport(report))
		return
	}

	provisioned := false
	if report.ImageDB == nil || !report.ImageDB.Exists {
		_, err = smltenant.BuildProvisionPlan(ctx, h.cfg, smltenant.ProvisionOptions{
			Tenant:        tenant,
			Template:      template,
			AdminDatabase: "postgres",
			Apply:         true,
		})
		if err != nil {
			// A concurrent request may have created the database after our first
			// readiness check. Re-verify once so the endpoint stays idempotent.
			after, verifyErr := smltenant.VerifyTenant(ctx, h.cfg, smltenant.VerifyOptions{
				Tenant:        tenant,
				Template:      template,
				AdminDatabase: "postgres",
			})
			if verifyErr == nil && after.OK {
				api.OK(c, gin.H{
					"provisioned": false,
					"readiness":   tenantReadinessFromReport(after),
				})
				return
			}
			api.Error(c, http.StatusBadGateway, "tenant_image_database_provision_failed", "image database provision failed", gin.H{"tenant": tenant, "imageDatabase": smltenant.ImageDatabaseName(tenant)})
			return
		}
		provisioned = true
	}

	createdMainSchema, err := smltenant.EnsureDocImagesSchema(ctx, h.cfg, tenant, template)
	if err != nil {
		api.Error(c, http.StatusBadGateway, "tenant_doc_images_schema_provision_failed", "tenant document image schema provision failed", gin.H{"tenant": tenant})
		return
	}
	if createdMainSchema {
		provisioned = true
	}
	createdImageSchema, err := smltenant.EnsureDocImagesSchema(ctx, h.cfg, smltenant.ImageDatabaseName(tenant), template)
	if err != nil {
		api.Error(c, http.StatusBadGateway, "tenant_image_schema_provision_failed", "image database schema provision failed", gin.H{"tenant": tenant, "imageDatabase": smltenant.ImageDatabaseName(tenant)})
		return
	}
	if createdImageSchema {
		provisioned = true
	}

	after, err := smltenant.VerifyTenant(ctx, h.cfg, smltenant.VerifyOptions{
		Tenant:        tenant,
		Template:      template,
		AdminDatabase: "postgres",
	})
	if err != nil {
		api.Error(c, http.StatusBadGateway, "tenant_readiness_failed", "tenant readiness check failed after provision", gin.H{"tenant": tenant})
		return
	}
	if !after.OK {
		api.Error(c, http.StatusFailedDependency, "tenant_still_not_ready", "tenant is still not ready after provision", tenantReadinessFromReport(after))
		return
	}
	api.OK(c, gin.H{
		"provisioned": provisioned,
		"readiness":   tenantReadinessFromReport(after),
	})
}

func tenantCanProvisionDocImages(report smltenant.VerifyReport) bool {
	hasProvisionableFailure := false
	for _, check := range report.Checks {
		if check.Status == smltenant.CheckOK {
			continue
		}
		switch check.Name {
		case "image_database", "tenant_sml_doc_images_table", "image_sml_doc_images_table":
			hasProvisionableFailure = true
		default:
			return false
		}
	}
	return hasProvisionableFailure
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
			case "tenant_sml_doc_images_table", "image_sml_doc_images_table":
				status = "doc_images_table_missing"
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
