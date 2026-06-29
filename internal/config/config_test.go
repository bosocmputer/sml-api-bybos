package config

import (
	"strings"
	"testing"
)

func TestDSNUsesTenantOverride(t *testing.T) {
	t.Setenv("SML_DB_HOST", "192.168.2.248")
	t.Setenv("SML_DB_PORT", "5432")
	t.Setenv("SML_DB_USER", "postgres")
	t.Setenv("SML_DB_PASSWORD", "default-pass")
	t.Setenv("SML_DB_SSLMODE", "disable")
	t.Setenv("SML_DB_HOST_AOY", "demserver.3bbddns.com")
	t.Setenv("SML_DB_PORT_AOY", "47309")
	t.Setenv("SML_DB_USER_AOY", "postgres")
	t.Setenv("SML_DB_PASSWORD_AOY", "sml")
	t.Setenv("SML_DB_SSLMODE_AOY", "disable")

	cfg := Load()

	got := cfg.DSN("aoy")
	for _, want := range []string{
		"host=demserver.3bbddns.com",
		"port=47309",
		"user=postgres",
		"password=sml",
		"dbname=aoy",
		"sslmode=disable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tenant override DSN = %q, missing %q", got, want)
		}
	}
}

func TestLogsDatabaseUsesBaseTenantOverride(t *testing.T) {
	t.Setenv("SML_DB_HOST", "192.168.2.248")
	t.Setenv("SML_DB_PORT", "5432")
	t.Setenv("SML_DB_USER", "postgres")
	t.Setenv("SML_DB_PASSWORD", "default-pass")
	t.Setenv("SML_DB_SSLMODE", "disable")
	t.Setenv("SML_DB_HOST_AOY", "demserver.3bbddns.com")
	t.Setenv("SML_DB_PORT_AOY", "47309")
	t.Setenv("SML_DB_USER_AOY", "postgres")
	t.Setenv("SML_DB_PASSWORD_AOY", "sml")
	t.Setenv("SML_DB_SSLMODE_AOY", "disable")

	cfg := Load()

	got := cfg.DSN("aoy_logs")
	for _, want := range []string{
		"host=demserver.3bbddns.com",
		"port=47309",
		"password=sml",
		"dbname=aoy_logs",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs DSN = %q, missing %q", got, want)
		}
	}
}

func TestDSNFallsBackToDefaultConnection(t *testing.T) {
	t.Setenv("SML_DB_HOST", "192.168.2.248")
	t.Setenv("SML_DB_PORT", "5432")
	t.Setenv("SML_DB_USER", "postgres")
	t.Setenv("SML_DB_PASSWORD", "default-pass")
	t.Setenv("SML_DB_SSLMODE", "disable")
	t.Setenv("SML_DB_HOST_AOY", "demserver.3bbddns.com")

	cfg := Load()

	got := cfg.DSN("sml1_2026")
	for _, want := range []string{
		"host=192.168.2.248",
		"port=5432",
		"password=default-pass",
		"dbname=sml1_2026",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("default DSN = %q, missing %q", got, want)
		}
	}
}
