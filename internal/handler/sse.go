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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/job"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/service"
)

// SSEHandler handles GET /archives/{job_id}/events.
//
// Each connection independently polls the job store on a fixed interval and
// writes an event whenever the job's state has changed since the last write
// (or immediately on connect, so a reconnecting client gets current state
// without delay). Disconnecting the client only stops this handler's
// goroutine; it never cancels the underlying archive job, which is tracked
// independently by the worker pool.
type SSEHandler struct {
	svc          *service.ArchiveService
	pollInterval time.Duration
	expiry       time.Duration
	baseURL      string
	shutdown     context.Context
	logger       *slog.Logger
}

// NewSSEHandler builds an SSEHandler. baseURL is prefixed to the download
// path in completed events; pass "" to emit a relative path.
func NewSSEHandler(svc *service.ArchiveService, pollInterval, expiry time.Duration, baseURL string, shutdown context.Context, logger *slog.Logger) *SSEHandler {
	return &SSEHandler{svc: svc, pollInterval: pollInterval, expiry: expiry, baseURL: baseURL, shutdown: shutdown, logger: logger}
}

// Events handles GET /archives/{job_id}/events, streaming progress,
// completed and error events until the job reaches a terminal state,
// the client disconnects, or the server shuts down.
func (h *SSEHandler) Events(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")

	j, err := h.svc.GetJob(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, job.ErrNotFound) {
			writeError(w, h.logger, http.StatusNotFound, "job not found")
			return
		}
		h.logger.Error("failed to load job for sse", "job_id", jobID, "error", err)
		writeError(w, h.logger, http.StatusInternalServerError, "failed to open event stream")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, h.logger, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var lastProgress model.ProgressEvent
	sentAny := false

	send := func(current *model.Job) (done bool) {
		switch current.Status {
		case model.JobStatusCompleted:
			writeSSEEvent(w, "completed", model.CompletedEvent{
				JobID:         current.ID,
				DownloadURL:   fmt.Sprintf("%s/archives/%s/download", h.baseURL, current.ID),
				DownloadToken: current.DownloadToken,
				ExpiresInSecs: int64(time.Until(current.ExpiresAt).Seconds()),
			})
			flusher.Flush()
			return true
		case model.JobStatusFailed, model.JobStatusExpired:
			msg := current.ErrorMessage
			if msg == "" {
				msg = "archive job failed"
			}
			writeSSEEvent(w, "error", model.ErrorEvent{Message: msg})
			flusher.Flush()
			return true
		default:
			progress := model.ProgressEvent{
				ProcessedFiles: current.ProcessedFiles,
				TotalFiles:     current.TotalFiles,
				ProcessedBytes: current.ProcessedBytes,
				TotalBytes:     current.TotalBytes,
			}
			if !sentAny || progress != lastProgress {
				writeSSEEvent(w, "progress", progress)
				flusher.Flush()
				lastProgress = progress
				sentAny = true
			}
			return false
		}
	}

	if send(j) {
		return
	}

	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.shutdown.Done():
			return
		case <-ticker.C:
			current, err := h.svc.GetJob(r.Context(), jobID)
			if err != nil {
				if errors.Is(err, job.ErrNotFound) {
					writeSSEEvent(w, "error", model.ErrorEvent{Message: "job no longer available"})
					flusher.Flush()
					return
				}
				continue
			}
			if send(current) {
				return
			}
		}
	}
}

// writeSSEEvent writes a single named SSE event with a JSON data line.
// The caller is responsible for flushing.
func writeSSEEvent(w http.ResponseWriter, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
