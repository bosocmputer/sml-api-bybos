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
	var template string
	var adminDB string
	var apply bool
	var jsonOnly bool
	var timeout time.Duration

	flag.StringVar(&tenant, "tenant", "", "SML tenant database that needs a matching _images database")
	flag.StringVar(&template, "template", "iampcoffee_images", "trusted _images database used as schema reference")
	flag.StringVar(&adminDB, "admin-db", "postgres", "database used for CREATE DATABASE and pg_database catalog checks")
	flag.BoolVar(&apply, "apply", false, "create the image database; default is dry-run only")
	flag.BoolVar(&jsonOnly, "json-only", false, "print JSON only")
	flag.DurationVar(&timeout, "timeout", 60*time.Second, "provision timeout")
	flag.Parse()

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	plan, err := smltenant.BuildProvisionPlan(ctx, cfg, smltenant.ProvisionOptions{
		Tenant:        tenant,
		Template:      template,
		AdminDatabase: adminDB,
		Apply:         apply,
	})
	if err != nil {
		exitWithError(err.Error())
	}

	if !jsonOnly {
		mode := "DRY-RUN"
		if apply {
			mode = "APPLIED"
		}
		fmt.Printf("PROVISION %s -> %s\n", plan.ImageDatabase, mode)
		fmt.Printf("  tenant: %s\n", plan.Tenant)
		fmt.Printf("  template: %s\n", plan.Template)
		if !apply {
			fmt.Println("  no changes were applied; pass --apply to create the database")
		}
		fmt.Println("  statements:")
		for _, stmt := range plan.Statements {
			fmt.Printf("    %s;\n", stmt)
		}
		fmt.Println()
		fmt.Println("JSON:")
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		exitWithError(err.Error())
	}
}

func exitWithError(message string) {
	fmt.Fprintln(os.Stderr, "provision-sml-image-db:", message)
	os.Exit(2)
}
