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

// Package server builds the HTTP server and routes for the archive service.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/config"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/handler"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/service"
)

// New builds the configured *http.Server with all routes wired.
//
// SERVER_READ_TIMEOUT is applied as ReadHeaderTimeout (bounds only how long
// a client may take to send request headers) and SERVER_IDLE_TIMEOUT as
// IdleTimeout (bounds idle time between requests on a keep-alive
// connection). The server intentionally leaves the whole-request
// ReadTimeout/WriteTimeout unset: those apply for the lifetime of the
// connection including body streaming, and would otherwise truncate large
// archive downloads and long-lived SSE event streams.
func New(
	cfg config.ServerConfig,
	svc *service.ArchiveService,
	archiveExpiry time.Duration,
	readiness *handler.Readiness,
	shutdown context.Context,
	logger *slog.Logger,
) *http.Server {
	mux := http.NewServeMux()

	archiveHandler := handler.NewArchiveHandler(svc, archiveExpiry, cfg.PublicBaseURL, logger)
	sseHandler := handler.NewSSEHandler(svc, 500*time.Millisecond, archiveExpiry, cfg.PublicBaseURL, shutdown, logger)
	downloadHandler := handler.NewDownloadHandler(svc, logger)
	healthHandler := handler.NewHealthHandler(readiness, logger)

	mux.Handle("POST /internal/archives", requireAPIToken(cfg.SelfAPIToken, logger)(http.HandlerFunc(archiveHandler.Create)))
	mux.HandleFunc("GET /archives/{job_id}/events", sseHandler.Events)
	mux.HandleFunc("GET /archives/{job_id}/download", downloadHandler.Download)
	mux.HandleFunc("GET /healthz", healthHandler.Healthz)
	mux.HandleFunc("GET /readyz", healthHandler.Readyz)

	return &http.Server{
		Addr:              cfg.Addr(),
		Handler:           requestLogger(logger)(cors(mux)),
		ReadHeaderTimeout: cfg.ReadTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}

// cors allows cross-origin requests from any domain. The request's Origin
// is echoed back (rather than "*") so credentialed browser requests work
// too. OPTIONS preflight requests are answered directly with 204 and never
// reach the mux, which has no OPTIONS routes and would otherwise reply 405.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if origin := r.Header.Get("Origin"); origin != "" {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
		} else {
			h.Set("Access-Control-Allow-Origin", "*")
		}
		h.Set("Access-Control-Expose-Headers", "Content-Disposition, Content-Length, Content-Type")

		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
				h.Set("Access-Control-Allow-Headers", reqHeaders)
				h.Add("Vary", "Access-Control-Request-Headers")
			} else {
				h.Set("Access-Control-Allow-Headers", "*")
			}
			h.Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// apiTokenHeader carries the shared secret for internal endpoints.
const apiTokenHeader = "X-API-Token"

// requireAPIToken returns middleware that rejects requests whose
// X-API-Token header does not match token with 401 Unauthorized. The
// comparison is constant-time so the secret cannot be recovered by timing.
func requireAPIToken(token string, logger *slog.Logger) func(http.Handler) http.Handler {
	want := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := []byte(r.Header.Get(apiTokenHeader))
			if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				if err := json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"}); err != nil {
					logger.Error("failed to write json response", "error", err)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger returns middleware that logs method, path, status and
// duration for every request once it completes.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// statusWriter wraps an http.ResponseWriter to record the response
// status code for requestLogger.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records status and forwards it to the wrapped writer.
func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush forwards to the underlying writer so streaming handlers (SSE) still
// see an http.Flusher through this wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
