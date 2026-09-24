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

package handler

import (
	"log/slog"
	"net/http"
	"sync/atomic"
)

// Readiness is a shared, thread-safe flag toggled by main during startup
// and graceful shutdown, and read by the /readyz handler.
type Readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a Readiness flag that starts out not ready.
func NewReadiness() *Readiness {
	return &Readiness{}
}

// Set updates the readiness flag.
func (r *Readiness) Set(ready bool) {
	r.ready.Store(ready)
}

// Get reports whether the service is ready to receive traffic.
func (r *Readiness) Get() bool {
	return r.ready.Load()
}

// HealthHandler serves /healthz and /readyz.
type HealthHandler struct {
	readiness *Readiness
	logger    *slog.Logger
}

// NewHealthHandler builds a HealthHandler backed by readiness.
func NewHealthHandler(readiness *Readiness, logger *slog.Logger) *HealthHandler {
	return &HealthHandler{readiness: readiness, logger: logger}
}

// statusBody is the JSON shape of health responses.
type statusBody struct {
	Status string `json:"status"`
}

// Healthz handles GET /healthz. It always responds 200 while the process
// is serving HTTP.
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.logger, http.StatusOK, statusBody{Status: "ok"})
}

// Readyz handles GET /readyz. It responds 200 once startup completes and
// 503 from the moment graceful shutdown begins.
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	if !h.readiness.Get() {
		writeJSON(w, h.logger, http.StatusServiceUnavailable, statusBody{Status: "not ready"})
		return
	}
	writeJSON(w, h.logger, http.StatusOK, statusBody{Status: "ready"})
}
