package persistence

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type StorageMode string

const (
	StorageJSON     StorageMode = "json"
	StorageDual     StorageMode = "dual"
	StoragePostgres StorageMode = "postgres"
)

type Config struct {
	Mode          StorageMode
	DatabaseURL   string
	MaxOpenConns  int
	MaxIdleConns  int
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisPrefix   string
	LockTTL       time.Duration
}

func LoadConfig() (Config, error) {
	mode := StorageMode(strings.ToLower(env("RUNNING_STORAGE_MODE", string(StorageJSON))))
	if mode != StorageJSON && mode != StorageDual && mode != StoragePostgres {
		return Config{}, fmt.Errorf("RUNNING_STORAGE_MODE must be json, dual, or postgres")
	}
	databaseURL := strings.TrimSpace(os.Getenv("RUNNING_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = postgresURLFromEnvironment()
	}
	cfg := Config{
		Mode:          mode,
		DatabaseURL:   databaseURL,
		MaxOpenConns:  envInt("RUNNING_DATABASE_MAX_OPEN_CONNS", 20),
		MaxIdleConns:  envInt("RUNNING_DATABASE_MAX_IDLE_CONNS", 5),
		RedisAddr:     strings.TrimSpace(os.Getenv("RUNNING_REDIS_ADDR")),
		RedisPassword: os.Getenv("RUNNING_REDIS_PASSWORD"),
		RedisDB:       envInt("RUNNING_REDIS_DB", 0),
		RedisPrefix:   env("RUNNING_REDIS_PREFIX", "running-tools:"),
		LockTTL:       time.Duration(envInt("RUNNING_REDIS_LOCK_TTL_SECONDS", 600)) * time.Second,
	}
	if mode != StorageJSON && cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("RUNNING_DATABASE_URL is required in %s mode", mode)
	}
	if cfg.MaxOpenConns < 1 || cfg.MaxIdleConns < 0 || cfg.MaxIdleConns > cfg.MaxOpenConns {
		return Config{}, fmt.Errorf("invalid PostgreSQL connection pool settings")
	}
	if cfg.RedisDB < 0 || cfg.LockTTL < 30*time.Second {
		return Config{}, fmt.Errorf("invalid Redis settings")
	}
	if !strings.HasSuffix(cfg.RedisPrefix, ":") {
		cfg.RedisPrefix += ":"
	}
	return cfg, nil
}

func postgresURLFromEnvironment() string {
	host := strings.TrimSpace(os.Getenv("RUNNING_POSTGRES_HOST"))
	if host == "" {
		return ""
	}
	port := env("RUNNING_POSTGRES_PORT", "5432")
	username := env("RUNNING_POSTGRES_USER", "running_tools")
	password := os.Getenv("RUNNING_POSTGRES_PASSWORD")
	database := env("RUNNING_POSTGRES_DB", "running_tools")
	dsn := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, port), Path: "/" + database}
	dsn.User = url.UserPassword(username, password)
	query := dsn.Query()
	query.Set("sslmode", env("RUNNING_POSTGRES_SSLMODE", "disable"))
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
