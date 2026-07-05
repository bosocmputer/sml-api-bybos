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
	Auth struct {
		MainDatabase          string
		Provider              string
		DataGroup             string
		ImageTemplateDatabase string
	}
	DB struct {
		Host            string
		Port            string
		User            string
		Password        string
		SSLMode         string
		MaxConns        int32
		MinConns        int32
		DefaultTenant   string              // used when no tenant header is present
		AllowedTenants  map[string]struct{} // empty = allow all (internal use only)
		TenantOverrides map[string]DBConnConfig
	}
	APIKeys []string // accepted in X-Api-Key header, guid header, or api_key query
}

type DBConnConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	SSLMode  string
}

func Load() *Config {
	_ = godotenv.Load()

	c := &Config{}
	c.Server.Port = getEnv("SERVER_PORT", "8200")
	c.Server.Host = getEnv("SERVER_HOST", "0.0.0.0")
	c.Auth.MainDatabase = strings.ToLower(strings.TrimSpace(getEnv("SML_AUTH_MAIN_DATABASE", "smlerpmainsmlgoh")))
	c.Auth.Provider = strings.ToLower(strings.TrimSpace(getEnv("SML_AUTH_PROVIDER", "smlgoh")))
	c.Auth.DataGroup = strings.ToLower(strings.TrimSpace(getEnv("SML_AUTH_DATAGROUP", "sml")))
	c.Auth.ImageTemplateDatabase = strings.ToLower(strings.TrimSpace(getEnv("SML_IMAGE_TEMPLATE_DATABASE", "iampcoffee_images")))

	c.DB.Host = getEnv("SML_DB_HOST", "192.168.2.248")
	c.DB.Port = getEnv("SML_DB_PORT", "5432")
	c.DB.User = getEnv("SML_DB_USER", "postgres")
	c.DB.Password = getEnv("SML_DB_PASSWORD", "sml")
	c.DB.SSLMode = getEnv("SML_DB_SSLMODE", "disable")
	c.DB.MaxConns = int32(getEnvInt("SML_DB_MAX_CONNS", 10))
	c.DB.MinConns = int32(getEnvInt("SML_DB_MIN_CONNS", 2))
	c.DB.DefaultTenant = strings.ToLower(strings.TrimSpace(getEnv("DEFAULT_TENANT", "")))
	c.DB.TenantOverrides = loadTenantDBOverrides(DBConnConfig{
		Host:     c.DB.Host,
		Port:     c.DB.Port,
		User:     c.DB.User,
		Password: c.DB.Password,
		SSLMode:  c.DB.SSLMode,
	})

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
	conn := c.DBConn(dbName)
	return "host=" + conn.Host +
		" port=" + conn.Port +
		" user=" + conn.User +
		" password=" + conn.Password +
		" dbname=" + dbName +
		" sslmode=" + conn.SSLMode
}

func (c *Config) DBConn(dbName string) DBConnConfig {
	tenant := strings.ToLower(strings.TrimSpace(dbName))
	if conn, ok := c.DB.TenantOverrides[tenant]; ok {
		return conn
	}
	if strings.HasSuffix(tenant, "_logs") {
		baseTenant := strings.TrimSuffix(tenant, "_logs")
		if conn, ok := c.DB.TenantOverrides[baseTenant]; ok {
			return conn
		}
	}
	return DBConnConfig{
		Host:     c.DB.Host,
		Port:     c.DB.Port,
		User:     c.DB.User,
		Password: c.DB.Password,
		SSLMode:  c.DB.SSLMode,
	}
}

func loadTenantDBOverrides(defaultConn DBConnConfig) map[string]DBConnConfig {
	tenants := map[string]struct{}{}
	prefixes := []string{
		"SML_DB_HOST_",
		"SML_DB_PORT_",
		"SML_DB_USER_",
		"SML_DB_PASSWORD_",
		"SML_DB_SSLMODE_",
	}
	for _, pair := range os.Environ() {
		key := strings.SplitN(pair, "=", 2)[0]
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				tenant := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(key, prefix)))
				if tenant != "" {
					tenants[tenant] = struct{}{}
				}
			}
		}
	}

	overrides := make(map[string]DBConnConfig, len(tenants))
	for tenant := range tenants {
		suffix := strings.ToUpper(tenant)
		conn := defaultConn
		if v := getEnv("SML_DB_HOST_"+suffix, ""); v != "" {
			conn.Host = v
		}
		if v := getEnv("SML_DB_PORT_"+suffix, ""); v != "" {
			conn.Port = v
		}
		if v := getEnv("SML_DB_USER_"+suffix, ""); v != "" {
			conn.User = v
		}
		if v := getEnv("SML_DB_PASSWORD_"+suffix, ""); v != "" {
			conn.Password = v
		}
		if v := getEnv("SML_DB_SSLMODE_"+suffix, ""); v != "" {
			conn.SSLMode = v
		}
		overrides[tenant] = conn
	}
	return overrides
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
