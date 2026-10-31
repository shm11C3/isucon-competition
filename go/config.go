package isureal

import (
	"net"
	"net/url"
	"os"
)

type Config struct {
	Region     string
	DBHost     string
	// ImageDBHost serves image bytes; when the new-image partition is UNLOGGED it must be the primary.
	ImageDBHost string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	LogLevel   string
	// ApplyBarrier commits writes locally and waits for standby apply before responding.
	ApplyBarrier bool
}

func ConfigFromEnv() *Config {
	return &Config{
		Region:       envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:       envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		ImageDBHost:  envOrDefault("ISUREAL_IMAGE_DB_HOST", ""),
		DBPort:       envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:       envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword:   envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:       envOrDefault("ISUREAL_DB_NAME", "isureal"),
		LogLevel:     envOrDefault("ISUREAL_LOG_LEVEL", "debug"),
		ApplyBarrier: envOrDefault("ISUREAL_APPLY_BARRIER", "1") != "0",
	}
}

func (c *Config) DatabaseDSN() string {
	return c.dsn(c.ApplyBarrier, "local")
}

// ImageDSN is the pool that reads image bytes. It points at the primary when
// ISUREAL_IMAGE_DB_HOST is set (the new-image partition is UNLOGGED and
// therefore not readable on standbys); otherwise it is the regular pool.
func (c *Config) ImageDSN() string {
	if c.ImageDBHost == "" || c.ImageDBHost == c.DBHost {
		return ""
	}
	d := *c
	d.DBHost = c.ImageDBHost
	return d.dsn(true, "local")
}

// BarrierDSN is the pool used by the apply barrier; its commits wait for standby apply.
func (c *Config) BarrierDSN() string {
	return c.dsn(true, "remote_apply")
}

func (c *Config) dsn(setSyncCommit bool, syncCommit string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   net.JoinHostPort(c.DBHost, c.DBPort),
		Path:   "/" + c.DBName,
	}
	if setSyncCommit {
		// pgx passes unknown URL parameters to the server as startup runtime parameters.
		// Writes commit locally (row locks released); the apply barrier waits for the standbys.
		u.RawQuery = "synchronous_commit=" + syncCommit
	}
	return u.String()
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
