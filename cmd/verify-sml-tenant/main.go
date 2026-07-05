package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"sml-api-bybos/internal/config"
	"sml-api-bybos/internal/smltenant"
)

func main() {
	var tenant string
	var allAllowed bool
	var template string
	var adminDB string
	var docNo string
	var jsonOnly bool
	var timeout time.Duration

	flag.StringVar(&tenant, "tenant", "", "SML tenant database to verify, for example stpt")
	flag.BoolVar(&allAllowed, "all-allowed", false, "verify all ALLOWED_TENANTS from environment")
	flag.StringVar(&template, "template", "iampcoffee_images", "trusted _images database used as schema reference")
	flag.StringVar(&adminDB, "admin-db", "postgres", "database used for pg_database catalog checks")
	flag.StringVar(&docNo, "doc-no", "", "optional document number to verify image rows")
	flag.BoolVar(&jsonOnly, "json-only", false, "print JSON only")
	flag.DurationVar(&timeout, "timeout", 45*time.Second, "verification timeout")
	flag.Parse()

	cfg := config.Load()
	tenants := []string{}
	if allAllowed {
		tenants = smltenant.AllowedTenants(cfg)
		if len(tenants) == 0 {
			exitWithError("ALLOWED_TENANTS is empty; pass --tenant instead")
		}
	} else {
		tenant = smltenant.NormalizeTenant(tenant)
		if tenant == "" {
			exitWithError("--tenant is required unless --all-allowed is set")
		}
		tenants = append(tenants, tenant)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	reports := make([]smltenant.VerifyReport, 0, len(tenants))
	overallOK := true
	for _, t := range tenants {
		report, err := smltenant.VerifyTenant(ctx, cfg, smltenant.VerifyOptions{
			Tenant:        t,
			Template:      template,
			AdminDatabase: adminDB,
			DocNo:         docNo,
		})
		if err != nil {
			exitWithError(err.Error())
		}
		reports = append(reports, report)
		if !report.OK {
			overallOK = false
		}
	}

	if !jsonOnly {
		for _, report := range reports {
			printSummary(report)
		}
		fmt.Println()
		fmt.Println("JSON:")
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(reports); err != nil {
		exitWithError(err.Error())
	}
	if !overallOK {
		os.Exit(1)
	}
}

func printSummary(report smltenant.VerifyReport) {
	status := "PASS"
	if !report.OK {
		status = "FAIL"
	}
	fmt.Printf("VERIFY %s -> %s\n", report.Tenant, status)
	for _, check := range report.Checks {
		fmt.Printf("  [%s] %s: %s\n", check.Status, check.Name, check.Message)
	}
	if report.MainRows != nil {
		fmt.Printf("  main rows: %d rows, %d with bytes, %d jpeg magic\n", report.MainRows.Rows, report.MainRows.RowsWithImageFile, report.MainRows.JPEGMagicRows)
	}
	if report.ImageRows != nil {
		fmt.Printf("  image rows: %d rows, %d with bytes, %d jpeg magic\n", report.ImageRows.Rows, report.ImageRows.RowsWithImageFile, report.ImageRows.JPEGMagicRows)
	}
}

func exitWithError(message string) {
	fmt.Fprintln(os.Stderr, "verify-sml-tenant:", message)
	os.Exit(2)
}
