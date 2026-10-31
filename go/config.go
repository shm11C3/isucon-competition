package isureal

import (
	"net"
	"net/url"
	"os"
)

type Config struct {
	Region     string
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	LogLevel   string
	// Post image bytes: served by the process with ImageStoreDir, reached via ImageStoreURL.
	ImageStoreDir   string
	ImageStoreURL   string
	ImageStoreToken string
	// ImageCachePeerURL is the GET process on this host whose cache receives uploaded bytes.
	ImageCachePeerURL string
	// LocalImageCache is off for write-only processes that never serve GET
	// /api/images, so their 128MiB image cache does not compete with GOMEMLIMIT.
	LocalImageCache bool
	// ApplyBarrier commits writes locally and waits for standby apply before responding.
	ApplyBarrier bool
}

func ConfigFromEnv() *Config {
	return &Config{
		Region:            envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:            envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		DBPort:            envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:            envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword:        envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:            envOrDefault("ISUREAL_DB_NAME", "isureal"),
		LogLevel:          envOrDefault("ISUREAL_LOG_LEVEL", "debug"),
		ImageStoreDir:     envOrDefault("ISUREAL_IMAGE_STORE_DIR", ""),
		ImageStoreURL:     envOrDefault("ISUREAL_IMAGE_STORE_URL", ""),
		ImageStoreToken:   envOrDefault("ISUREAL_IMAGE_STORE_TOKEN", ""),
		ImageCachePeerURL: envOrDefault("ISUREAL_IMAGE_CACHE_PEER_URL", ""),
		LocalImageCache:   envOrDefault("ISUREAL_IMAGE_CACHE_LOCAL", "1") != "0",
		ApplyBarrier:      envOrDefault("ISUREAL_APPLY_BARRIER", "1") != "0",
	}
}

func (c *Config) DatabaseDSN() string {
	return c.dsn(c.ApplyBarrier, "local")
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
