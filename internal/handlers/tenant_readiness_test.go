package handlers

import (
	"testing"

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
