package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Server struct {
		Port string
		Host string
	}
	DB struct {
		Host          string
		Port          string
		User          string
		Password      string
		SSLMode       string
		MaxConns      int32
		MinConns      int32
		DefaultTenant  string            // used when no tenant header is present
		AllowedTenants map[string]struct{} // empty = allow all (internal use only)
	}
	APIKeys []string // accepted in X-Api-Key header, guid header, or api_key query
}

func Load() *Config {
	_ = godotenv.Load()

	c := &Config{}
	c.Server.Port = getEnv("SERVER_PORT", "8200")
	c.Server.Host = getEnv("SERVER_HOST", "0.0.0.0")

	c.DB.Host = getEnv("SML_DB_HOST", "192.168.2.248")
	c.DB.Port = getEnv("SML_DB_PORT", "5432")
	c.DB.User = getEnv("SML_DB_USER", "postgres")
	c.DB.Password = getEnv("SML_DB_PASSWORD", "sml")
	c.DB.SSLMode = getEnv("SML_DB_SSLMODE", "disable")
	c.DB.MaxConns = int32(getEnvInt("SML_DB_MAX_CONNS", 10))
	c.DB.MinConns = int32(getEnvInt("SML_DB_MIN_CONNS", 2))
	c.DB.DefaultTenant = strings.ToLower(strings.TrimSpace(getEnv("DEFAULT_TENANT", "")))

	// ALLOWED_TENANTS=sml1,sml1_2026,stp1  (empty = allow all)
	c.DB.AllowedTenants = make(map[string]struct{})
	if raw := getEnv("ALLOWED_TENANTS", ""); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.ToLower(strings.TrimSpace(t))
			if t != "" {
				c.DB.AllowedTenants[t] = struct{}{}
			}
		}
	}

	// API_KEYS are also used as accepted guid values for BillFlow legacy auth
	raw := getEnv("API_KEYS", "dev-key")
	for _, k := range strings.Split(raw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			c.APIKeys = append(c.APIKeys, k)
		}
	}
	return c
}

func (c *Config) DSN(dbName string) string {
	return "host=" + c.DB.Host +
		" port=" + c.DB.Port +
		" user=" + c.DB.User +
		" password=" + c.DB.Password +
		" dbname=" + dbName +
		" sslmode=" + c.DB.SSLMode
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
