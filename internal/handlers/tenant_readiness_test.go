package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"sml-api-bybos/internal/smltenant"
)

func TestTenantCanProvisionDocImages(t *testing.T) {
	report := smltenant.VerifyReport{
		Checks: []smltenant.Check{
			{Name: "main_database", Status: smltenant.CheckOK},
			{Name: "image_database", Status: smltenant.CheckFail},
			{Name: "tenant_sml_doc_images_table", Status: smltenant.CheckFail},
			{Name: "template_database", Status: smltenant.CheckOK},
		},
	}
	if !tenantCanProvisionDocImages(report) {
		t.Fatal("expected missing image DB/table report to be provisionable")
	}
}

func TestTenantReadinessClassifiesPostgresDataCorruptionWithoutLeakingDetails(t *testing.T) {
	report := smltenant.VerifyReport{
		Tenant:        "test",
		ImageDatabase: "test_images",
		Cause:         &pgconn.PgError{Code: "XX001", Message: "invalid page in block 0 of internal relation"},
		Checks:        []smltenant.Check{{Name: "tenant_database_connection", Status: smltenant.CheckFail}},
	}
	got := tenantReadinessFromReport(report)
	if got.Status != "main_db_corrupted" || len(got.Issues) != 1 {
		t.Fatalf("readiness = %+v", got)
	}
	if got.Issues[0].Code != "main_db_corrupted" || got.Issues[0].Database != "test" {
		t.Fatalf("issue = %+v", got.Issues[0])
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsAny(string(encoded), "invalid page", "internal relation") {
		t.Fatalf("response leaked PostgreSQL internals: %s", encoded)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func TestTenantReadinessReportsEveryFailureWithOwner(t *testing.T) {
	report := smltenant.VerifyReport{
		Tenant:        "test",
		ImageDatabase: "test_images",
		Checks: []smltenant.Check{
			{Name: "tenant_database_connection", Status: smltenant.CheckFail, Message: "main database cannot be reached"},
			{Name: "image_sml_doc_images_indexes", Status: smltenant.CheckFail, Message: "indexes do not match template"},
		},
	}

	payload, err := json.Marshal(tenantReadinessFromReport(report))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Status string `json:"status"`
		Issues []struct {
			Code     string `json:"code"`
			Database string `json:"database"`
			Owner    string `json:"owner"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "main_db_unreachable" {
		t.Fatalf("status = %q, want main_db_unreachable", got.Status)
	}
	if len(got.Issues) != 2 {
		t.Fatalf("issues = %#v, want 2 failures", got.Issues)
	}
	if got.Issues[0].Database != "test" || got.Issues[0].Owner != "sml_erp" {
		t.Fatalf("main database issue = %#v", got.Issues[0])
	}
	if got.Issues[1].Code != "image_schema_indexes_mismatch" || got.Issues[1].Database != "test_images" {
		t.Fatalf("image schema issue = %#v", got.Issues[1])
	}
}

func TestReadinessIssueFromCheckClassifiesSupportedFailures(t *testing.T) {
	report := smltenant.VerifyReport{Tenant: "test", ImageDatabase: "test_images", Template: "vrh_images"}
	tests := []struct {
		checkName string
		code      string
		status    string
		database  string
		owner     string
	}{
		{checkName: "admin_database_connection", code: "readiness_service_unavailable", status: "readiness_service_unavailable", owner: "infrastructure"},
		{checkName: "main_database", code: "main_db_missing", status: "main_db_missing", database: "test", owner: "sml_erp"},
		{checkName: "image_database", code: "image_db_missing", status: "image_db_missing", database: "test_images", owner: "sml_erp"},
		{checkName: "template_database", code: "template_db_missing", status: "template_not_ready", database: "vrh_images", owner: "paperless"},
		{checkName: "tenant_database_connection", code: "main_db_unreachable", status: "main_db_unreachable", database: "test", owner: "sml_erp"},
		{checkName: "image_database_connection", code: "image_db_unreachable", status: "image_db_unreachable", database: "test_images", owner: "sml_erp"},
		{checkName: "tenant_schema_inspection", code: "main_schema_inspection_failed", status: "main_schema_inspection_failed", database: "test", owner: "sml_erp"},
		{checkName: "image_schema_inspection", code: "image_schema_inspection_failed", status: "image_schema_inspection_failed", database: "test_images", owner: "sml_erp"},
		{checkName: "tenant_sml_doc_images_table", code: "main_doc_images_table_missing", status: "doc_images_table_missing", database: "test", owner: "sml_erp"},
		{checkName: "image_sml_doc_images_columns", code: "image_schema_columns_mismatch", status: "schema_mismatch", database: "test_images", owner: "sml_erp"},
		{checkName: "image_sml_doc_images_sequence", code: "image_schema_sequence_mismatch", status: "schema_mismatch", database: "test_images", owner: "sml_erp"},
		{checkName: "image_sml_doc_images_constraints", code: "image_schema_constraints_mismatch", status: "schema_mismatch", database: "test_images", owner: "sml_erp"},
		{checkName: "image_sml_doc_images_indexes", code: "image_schema_indexes_mismatch", status: "schema_mismatch", database: "test_images", owner: "sml_erp"},
		{checkName: "tenant_database_connection_timeout", code: "verification_timeout", status: "verification_timeout", database: "test", owner: "sml_erp"},
	}
	for _, tc := range tests {
		t.Run(tc.checkName, func(t *testing.T) {
			issue, status := readinessIssueFromCheck(report, smltenant.Check{Name: tc.checkName, Status: smltenant.CheckFail})
			if issue.Code != tc.code || status != tc.status || issue.Database != tc.database || issue.Owner != tc.owner {
				t.Fatalf("issue=%+v status=%q", issue, status)
			}
		})
	}
}

func TestTenantCanProvisionDocImagesRejectsOtherFailures(t *testing.T) {
	tests := []struct {
		name   string
		checks []smltenant.Check
	}{
		{
			name: "main database missing",
			checks: []smltenant.Check{
				{Name: "main_database", Status: smltenant.CheckFail},
				{Name: "image_database", Status: smltenant.CheckFail},
			},
		},
		{
			name: "schema mismatch",
			checks: []smltenant.Check{
				{Name: "image_sml_doc_images_columns", Status: smltenant.CheckFail},
			},
		},
		{
			name:   "ready tenant",
			checks: []smltenant.Check{{Name: "main_database", Status: smltenant.CheckOK}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tenantCanProvisionDocImages(smltenant.VerifyReport{Checks: tc.checks}) {
				t.Fatal("expected report to be rejected")
			}
		})
	}
}
