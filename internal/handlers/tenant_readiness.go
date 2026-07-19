package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

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
	logTenantReadinessCause(report)
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
	logTenantReadinessCause(report)
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

type tenantReadinessIssue struct {
	Code     string `json:"code"`
	Database string `json:"database,omitempty"`
	Owner    string `json:"owner"`
	Message  string `json:"message"`
}

type tenantReadinessResponse struct {
	OK            bool                   `json:"ok"`
	Status        string                 `json:"status"`
	Message       string                 `json:"message"`
	Tenant        string                 `json:"tenant"`
	ImageDatabase string                 `json:"imageDatabase"`
	Template      string                 `json:"template"`
	Checks        []smltenant.Check      `json:"checks"`
	Issues        []tenantReadinessIssue `json:"issues,omitempty"`
}

func tenantReadinessFromReport(report smltenant.VerifyReport) tenantReadinessResponse {
	status := "ready"
	message := "tenant is ready"
	issues := make([]tenantReadinessIssue, 0)
	if !report.OK {
		status = "schema_mismatch"
		message = "tenant image schema is not ready"
		for _, check := range report.Checks {
			if check.Status == smltenant.CheckOK {
				continue
			}
			issue, issueStatus := readinessIssueFromCheck(report, check)
			issues = append(issues, issue)
			if len(issues) == 1 {
				status = issueStatus
				message = issue.Message
			}
		}
	}
	return tenantReadinessResponse{
		OK:            report.OK,
		Status:        status,
		Message:       message,
		Tenant:        report.Tenant,
		ImageDatabase: report.ImageDatabase,
		Template:      report.Template,
		Checks:        report.Checks,
		Issues:        issues,
	}
}

func readinessIssueFromCheck(report smltenant.VerifyReport, check smltenant.Check) (tenantReadinessIssue, string) {
	name := check.Name
	issue := tenantReadinessIssue{Code: "schema_mismatch", Owner: "sml_erp", Message: "schema ตารางรูปเอกสารไม่ตรงกับมาตรฐาน"}
	status := "schema_mismatch"
	switch {
	case strings.HasSuffix(name, "_timeout"):
		issue.Code = "verification_timeout"
		issue.Owner = "infrastructure"
		issue.Message = "การตรวจสอบฐานข้อมูลใช้เวลานานเกินกำหนด"
		switch {
		case strings.HasPrefix(name, "tenant_"):
			issue.Database = report.Tenant
			issue.Owner = "sml_erp"
			issue.Message = "การตรวจสอบฐานข้อมูล " + report.Tenant + " ใช้เวลานานเกินกำหนด"
		case strings.HasPrefix(name, "image_"):
			issue.Database = report.ImageDatabase
			issue.Owner = "sml_erp"
			issue.Message = "การตรวจสอบฐานข้อมูล " + report.ImageDatabase + " ใช้เวลานานเกินกำหนด"
		case strings.HasPrefix(name, "template_"):
			issue.Database = report.Template
			issue.Owner = "paperless"
			issue.Message = "การตรวจสอบฐานข้อมูลต้นแบบ " + report.Template + " ใช้เวลานานเกินกำหนด"
		}
		status = "verification_timeout"
	case name == "admin_database_connection" || name == "admin_database_inspection":
		issue.Code = "readiness_service_unavailable"
		issue.Owner = "infrastructure"
		issue.Message = "ระบบตรวจสอบรายการฐานข้อมูล PostgreSQL ใช้งานไม่ได้"
		status = "readiness_service_unavailable"
	case name == "main_database":
		issue.Code = "main_db_missing"
		issue.Database = report.Tenant
		issue.Message = "ไม่พบฐานข้อมูล SML หลัก " + report.Tenant
		status = "main_db_missing"
	case name == "image_database":
		issue.Code = "image_db_missing"
		issue.Database = report.ImageDatabase
		issue.Message = "ไม่พบฐานข้อมูลรูปเอกสาร " + report.ImageDatabase
		status = "image_db_missing"
	case name == "template_database":
		issue.Code = "template_db_missing"
		issue.Database = report.Template
		issue.Owner = "paperless"
		issue.Message = "ไม่พบฐานข้อมูลต้นแบบสำหรับตรวจ schema " + report.Template
		status = "template_not_ready"
	case name == "template_database_connection" || name == "template_schema_inspection" || name == "template_sml_doc_images":
		issue.Code = "template_not_ready"
		issue.Database = report.Template
		issue.Owner = "paperless"
		issue.Message = "ฐานข้อมูลต้นแบบ " + report.Template + " เปิดใช้งานหรือตรวจ schema ไม่ได้"
		status = "template_not_ready"
	case name == "tenant_database_connection":
		issue.Database = report.Tenant
		failureCode, failureMessage := databaseOperationalFailure(report.Cause)
		issue.Code = "main_db_" + failureCode
		issue.Message = "ฐานข้อมูล SML หลัก " + report.Tenant + " " + failureMessage
		status = issue.Code
	case name == "image_database_connection":
		issue.Database = report.ImageDatabase
		failureCode, failureMessage := databaseOperationalFailure(report.Cause)
		issue.Code = "image_db_" + failureCode
		issue.Message = "ฐานข้อมูลรูปเอกสาร " + report.ImageDatabase + " " + failureMessage
		status = issue.Code
	case strings.HasPrefix(name, "tenant_") && strings.Contains(name, "inspection"):
		issue.Code = "main_schema_inspection_failed"
		issue.Database = report.Tenant
		issue.Message = "ตรวจสอบข้อมูลหรือ schema ในฐาน " + report.Tenant + " ไม่ได้ อาจเกิดจากสิทธิ์ไม่พอหรือฐานข้อมูลเสียหาย"
		status = "main_schema_inspection_failed"
	case strings.HasPrefix(name, "image_") && strings.Contains(name, "inspection"):
		issue.Code = "image_schema_inspection_failed"
		issue.Database = report.ImageDatabase
		issue.Message = "ตรวจสอบข้อมูลหรือ schema ในฐาน " + report.ImageDatabase + " ไม่ได้ อาจเกิดจากสิทธิ์ไม่พอหรือฐานข้อมูลเสียหาย"
		status = "image_schema_inspection_failed"
	case name == "tenant_sml_doc_images_table":
		issue.Code = "main_doc_images_table_missing"
		issue.Database = report.Tenant
		issue.Message = "ฐานข้อมูล " + report.Tenant + " ไม่มีตาราง public.sml_doc_images"
		status = "doc_images_table_missing"
	case name == "image_sml_doc_images_table":
		issue.Code = "image_doc_images_table_missing"
		issue.Database = report.ImageDatabase
		issue.Message = "ฐานข้อมูล " + report.ImageDatabase + " ไม่มีตาราง public.sml_doc_images"
		status = "doc_images_table_missing"
	case strings.HasPrefix(name, "tenant_sml_doc_images_"):
		component := strings.TrimPrefix(name, "tenant_sml_doc_images_")
		issue.Code = "main_schema_" + component + "_mismatch"
		issue.Database = report.Tenant
		issue.Message = schemaMismatchMessage(report.Tenant, component)
	case strings.HasPrefix(name, "image_sml_doc_images_"):
		component := strings.TrimPrefix(name, "image_sml_doc_images_")
		issue.Code = "image_schema_" + component + "_mismatch"
		issue.Database = report.ImageDatabase
		issue.Message = schemaMismatchMessage(report.ImageDatabase, component)
	}
	return issue, status
}

func schemaMismatchMessage(database, component string) string {
	componentLabel := map[string]string{
		"columns":     "คอลัมน์/ชนิดข้อมูล",
		"sequence":    "sequence ของ roworder",
		"constraints": "constraint/primary key",
		"indexes":     "index",
	}[component]
	if componentLabel == "" {
		componentLabel = "schema"
	}
	return componentLabel + " ของ public.sml_doc_images ในฐาน " + database + " ไม่ตรงกับมาตรฐาน"
}

func databaseOperationalFailure(cause error) (string, string) {
	var pgErr *pgconn.PgError
	if !errors.As(cause, &pgErr) {
		return "unreachable", "เปิดใช้งานหรือเชื่อมต่อไม่ได้"
	}
	switch pgErr.Code {
	case "XX001", "XX002":
		return "corrupted", "ตรวจพบความเสียหายของข้อมูล PostgreSQL ต้องตรวจสอบหรือกู้คืนจาก backup"
	case "42501", "28000", "28P01":
		return "permission_denied", "ปฏิเสธสิทธิ์การเชื่อมต่อหรืออ่าน schema"
	case "53300":
		return "connection_limit", "มี connection เต็มหรือเกินขีดจำกัด"
	case "57P03", "55006":
		return "temporarily_unavailable", "ยังไม่พร้อมรับการเชื่อมต่อหรืออยู่ระหว่าง maintenance"
	default:
		return "unreachable", "เปิดใช้งานหรือเชื่อมต่อไม่ได้"
	}
}

func logTenantReadinessCause(report smltenant.VerifyReport) {
	if report.Cause == nil {
		return
	}
	slog.Warn("SML tenant readiness operational failure", "tenant", report.Tenant, "imageDatabase", report.ImageDatabase, "error", report.Cause)
}
