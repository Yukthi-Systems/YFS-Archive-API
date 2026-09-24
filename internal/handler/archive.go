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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/service"
)

// ArchiveHandler handles POST /internal/archives.
type ArchiveHandler struct {
	svc     *service.ArchiveService
	expiry  time.Duration
	baseURL string
	logger  *slog.Logger
}

// NewArchiveHandler builds an ArchiveHandler. expiry is reported to
// clients as expires_in; baseURL is prefixed to the events URL (empty
// yields a relative URL).
func NewArchiveHandler(svc *service.ArchiveService, expiry time.Duration, baseURL string, logger *slog.Logger) *ArchiveHandler {
	return &ArchiveHandler{svc: svc, expiry: expiry, baseURL: baseURL, logger: logger}
}

// createArchiveResponse is the 202 Accepted body of POST
// /internal/archives.
type createArchiveResponse struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	EventsURL string `json:"events_url"`
	ExpiresIn int64  `json:"expires_in"`
}

// maxCreateBodyBytes bounds the request body read for POST
// /internal/archives. The payload is two short strings; anything past a
// generous margin is either a mistake or an attempt to make the server
// buffer an oversized body.
const maxCreateBodyBytes = 1 << 20 // 1 MiB

// Create handles POST /internal/archives: it decodes the request, queues
// a job and responds 202 Accepted with the job ID and events URL.
func (h *ArchiveHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCreateBodyBytes)

	var req model.ArchiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, h.logger, http.StatusBadRequest, "invalid request body")
		return
	}

	j, err := h.svc.CreateJob(r.Context(), req)
	if err != nil {
		h.handleCreateError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusAccepted, createArchiveResponse{
		JobID:     j.ID,
		Status:    string(j.Status),
		EventsURL: fmt.Sprintf("%s/archives/%s/events", h.baseURL, j.ID),
		ExpiresIn: int64(h.expiry.Seconds()),
	})
}

// handleCreateError maps service errors to HTTP status codes and safe
// client-facing messages.
func (h *ArchiveHandler) handleCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrValidation):
		writeError(w, h.logger, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrFolderNotFound):
		writeError(w, h.logger, http.StatusNotFound, "root folder not found")
	case errors.Is(err, service.ErrQueueFull):
		writeError(w, h.logger, http.StatusServiceUnavailable, "archive service is busy, try again shortly")
	case errors.Is(err, service.ErrNotAccepting):
		writeError(w, h.logger, http.StatusServiceUnavailable, "archive service is shutting down")
	default:
		h.logger.Error("failed to create archive job", "error", err)
		writeError(w, h.logger, http.StatusInternalServerError, "archive creation failed")
	}
}
