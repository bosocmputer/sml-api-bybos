package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Server struct {
		Port string
		Host string
	}
	DB struct {
		Host     string
		Port     string
		User     string
		Password string
		SSLMode  string
	}
	APIKeys []string // comma-separated in env
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
