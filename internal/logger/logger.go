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

// Package logger builds a structured, rotation-aware slog.Logger from the
// application's LogConfig.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/config"
)

// New builds a slog.Logger according to cfg. For LogOutputFile it wires in
// lumberjack for size/age/backup-count based rotation and optional gzip
// compression of rotated files. The returned io.Closer should be closed
// during shutdown to flush/close the underlying file, if any.
func New(cfg config.LogConfig) (*slog.Logger, io.Closer, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}

	var writer io.Writer
	var closer io.Closer = nopCloser{}

	switch cfg.Output {
	case config.LogOutputStdout, "":
		writer = os.Stdout
	case config.LogOutputStderr:
		writer = os.Stderr
	case config.LogOutputFile:
		lj := &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAgeDays,
			Compress:   cfg.Compress,
		}
		writer = lj
		closer = lj
	default:
		return nil, nil, fmt.Errorf("unknown LOG_OUTPUT %q", cfg.Output)
	}

	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: level,
	})

	return slog.New(handler), closer, nil
}

// parseLevel converts a LOG_LEVEL value to a slog.Level. An empty value
// means info.
func parseLevel(level string) (slog.Level, error) {
	switch level {
	case "debug", "DEBUG":
		return slog.LevelDebug, nil
	case "info", "INFO", "":
		return slog.LevelInfo, nil
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn, nil
	case "error", "ERROR":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid LOG_LEVEL %q", level)
	}
}

// nopCloser is the io.Closer returned for stdout/stderr output, which
// must not be closed.
type nopCloser struct{}

// Close does nothing and returns nil.
func (nopCloser) Close() error { return nil }
