// Copyright (C) 2026 Yukthi Systems Private Limited
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License version 3
// as published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// version 3 along with this program. If not, see
// <https://www.gnu.org/licenses/>.

// Package config centralizes all environment-derived configuration for the
// archive service. No other package should read environment variables
// directly; everything flows through the typed structs defined here.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the complete, validated runtime configuration of the service.
// Build it with Load.
type Config struct {
	App      AppConfig
	Server   ServerConfig
	Database DatabaseConfig
	Log      LogConfig
	Archive  ArchiveConfig
	Storage  StorageConfig
}

// AppConfig holds application-wide metadata.
type AppConfig struct {
	Env string
}

// ServerConfig controls the HTTP listener and its timeouts.
type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	// PublicBaseURL is the externally reachable origin (scheme://host[:port])
	// used to build absolute download URLs. Empty means relative URLs.
	PublicBaseURL string
}

// Addr returns the host:port listen address.
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// DatabaseConfig controls the PostgreSQL connection pool.
type DatabaseConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// LogLevel is a textual slog level (debug, info, warn, error).
type LogLevel string

// LogOutput selects where log records are written.
type LogOutput string

const (
	LogOutputStdout LogOutput = "stdout"
	LogOutputStderr LogOutput = "stderr"
	LogOutputFile   LogOutput = "file"
)

// LogConfig controls log verbosity, destination and file rotation.
type LogConfig struct {
	Level      string
	Output     LogOutput
	File       string
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

// ArchiveConfig controls archive generation, retention and the worker
// pool.
type ArchiveConfig struct {
	TempDir         string
	MaxSizeBytes    int64
	Expiry          time.Duration
	Workers         int
	QueueSize       int
	CleanupInterval time.Duration
	FileConcurrency int
}

// StorageConfig controls outbound requests to storage servers.
type StorageConfig struct {
	RequestTimeout time.Duration
}

// Load reads a .env file if present (missing files are not an error), then
// builds a Config from the process environment, applying defaults and
// validating required fields.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	cfg := &Config{
		App: AppConfig{
			Env: getString("APP_ENV", "development"),
		},
		Server: ServerConfig{
			Host:          getString("SERVER_HOST", "0.0.0.0"),
			Port:          0,
			PublicBaseURL: strings.TrimRight(getString("SERVER_PUBLIC_BASE_URL", ""), "/"),
		},
		Database: DatabaseConfig{
			URL: getString("DATABASE_URL", ""),
		},
		Log: LogConfig{
			Level:  getString("LOG_LEVEL", "info"),
			Output: LogOutput(getString("LOG_OUTPUT", "stdout")),
			File:   getString("LOG_FILE", ""),
		},
		Archive: ArchiveConfig{
			TempDir: getString("ARCHIVE_TEMP_DIR", ""),
		},
	}

	var err error
	if cfg.Server.Port, err = getInt("SERVER_PORT", 8080); err != nil {
		return nil, err
	}
	if cfg.Server.ReadTimeout, err = getDuration("SERVER_READ_TIMEOUT", 15*time.Second); err != nil {
		return nil, err
	}
	if cfg.Server.WriteTimeout, err = getDuration("SERVER_WRITE_TIMEOUT", 15*time.Second); err != nil {
		return nil, err
	}
	if cfg.Server.IdleTimeout, err = getDuration("SERVER_IDLE_TIMEOUT", 60*time.Second); err != nil {
		return nil, err
	}
	if cfg.Server.ShutdownTimeout, err = getDuration("SERVER_SHUTDOWN_TIMEOUT", 30*time.Second); err != nil {
		return nil, err
	}

	maxConns, err := getInt("DATABASE_MAX_CONNS", 20)
	if err != nil {
		return nil, err
	}
	cfg.Database.MaxConns = int32(maxConns)

	minConns, err := getInt("DATABASE_MIN_CONNS", 5)
	if err != nil {
		return nil, err
	}
	cfg.Database.MinConns = int32(minConns)

	if cfg.Database.MaxConnLifetime, err = getDuration("DATABASE_MAX_CONN_LIFETIME", 30*time.Minute); err != nil {
		return nil, err
	}
	if cfg.Database.MaxConnIdleTime, err = getDuration("DATABASE_MAX_CONN_IDLE_TIME", 5*time.Minute); err != nil {
		return nil, err
	}

	if cfg.Log.MaxSizeMB, err = getInt("LOG_MAX_SIZE_MB", 100); err != nil {
		return nil, err
	}
	if cfg.Log.MaxBackups, err = getInt("LOG_MAX_BACKUPS", 10); err != nil {
		return nil, err
	}
	if cfg.Log.MaxAgeDays, err = getInt("LOG_MAX_AGE_DAYS", 30); err != nil {
		return nil, err
	}
	if cfg.Log.Compress, err = getBool("LOG_COMPRESS", true); err != nil {
		return nil, err
	}

	if cfg.Archive.MaxSizeBytes, err = getInt64("ARCHIVE_MAX_SIZE_BYTES", 4*1024*1024*1024); err != nil {
		return nil, err
	}
	if cfg.Archive.Expiry, err = getDuration("ARCHIVE_EXPIRY", time.Hour); err != nil {
		return nil, err
	}
	if cfg.Archive.Workers, err = getInt("ARCHIVE_WORKERS", 4); err != nil {
		return nil, err
	}
	if cfg.Archive.QueueSize, err = getInt("ARCHIVE_QUEUE_SIZE", 100); err != nil {
		return nil, err
	}
	if cfg.Archive.CleanupInterval, err = getDuration("ARCHIVE_CLEANUP_INTERVAL", 10*time.Minute); err != nil {
		return nil, err
	}
	if cfg.Archive.FileConcurrency, err = getInt("ARCHIVE_FILE_CONCURRENCY", 4); err != nil {
		return nil, err
	}

	if cfg.Storage.RequestTimeout, err = getDuration("STORAGE_REQUEST_TIMEOUT", 30*time.Second); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate enforces required fields and value ranges that cannot be
// expressed as simple defaults.
func (c *Config) validate() error {
	if c.Database.URL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.Archive.TempDir == "" {
		return fmt.Errorf("ARCHIVE_TEMP_DIR is required")
	}
	if c.Archive.MaxSizeBytes <= 0 {
		return fmt.Errorf("ARCHIVE_MAX_SIZE_BYTES must be positive")
	}
	if c.Archive.Workers <= 0 {
		return fmt.Errorf("ARCHIVE_WORKERS must be positive")
	}
	if c.Archive.QueueSize <= 0 {
		return fmt.Errorf("ARCHIVE_QUEUE_SIZE must be positive")
	}
	if c.Server.PublicBaseURL != "" {
		u, err := url.Parse(c.Server.PublicBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("SERVER_PUBLIC_BASE_URL must be an absolute http(s) URL, got %q", c.Server.PublicBaseURL)
		}
	}
	if c.Log.Output == LogOutputFile && c.Log.File == "" {
		return fmt.Errorf("LOG_FILE is required when LOG_OUTPUT=file")
	}
	return nil
}

// getString returns the value of the environment variable key, or def
// if it is unset or empty.
func getString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// getInt parses the environment variable key as an int, returning def
// if it is unset or empty.
func getInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid int for %s: %w", key, err)
	}
	return n, nil
}

// getInt64 parses the environment variable key as an int64, returning
// def if it is unset or empty.
func getInt64(key string, def int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid int64 for %s: %w", key, err)
	}
	return n, nil
}

// getBool parses the environment variable key with strconv.ParseBool,
// returning def if it is unset or empty.
func getBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid bool for %s: %w", key, err)
	}
	return b, nil
}

// getDuration parses the environment variable key with
// time.ParseDuration (e.g. "30s", "5m"), returning def if it is unset
// or empty.
func getDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %w", key, err)
	}
	return d, nil
}
