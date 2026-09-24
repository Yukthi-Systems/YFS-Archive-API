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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/job"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/service"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/token"
)

// DownloadHandler handles GET /archives/{job_id}/download?token=...
type DownloadHandler struct {
	svc    *service.ArchiveService
	logger *slog.Logger
}

// NewDownloadHandler builds a DownloadHandler.
func NewDownloadHandler(svc *service.ArchiveService, logger *slog.Logger) *DownloadHandler {
	return &DownloadHandler{svc: svc, logger: logger}
}

// Download handles GET /archives/{job_id}/download. It responds 401 for
// a missing, invalid or expired token, 404 for an unknown job, 409 if
// the job has not completed, and otherwise streams the archive.
func (h *DownloadHandler) Download(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	tok := r.URL.Query().Get("token")

	if tok == "" {
		writeError(w, h.logger, http.StatusUnauthorized, "missing download token")
		return
	}

	j, err := h.svc.GetJob(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, job.ErrNotFound) {
			writeError(w, h.logger, http.StatusNotFound, "job not found")
			return
		}
		h.logger.Error("failed to load job for download", "job_id", jobID, "error", err)
		writeError(w, h.logger, http.StatusInternalServerError, "download failed")
		return
	}

	if j.Status != model.JobStatusCompleted {
		writeError(w, h.logger, http.StatusConflict, "archive is not ready for download")
		return
	}

	if err := h.svc.ValidateDownloadToken(r.Context(), jobID, tok); err != nil {
		switch {
		case errors.Is(err, token.ErrTokenExpired):
			writeError(w, h.logger, http.StatusUnauthorized, "download token expired")
		default:
			writeError(w, h.logger, http.StatusUnauthorized, "invalid download token")
		}
		return
	}

	f, err := h.svc.OpenArchive(j.ID)
	if err != nil {
		h.logger.Error("failed to open archive for download", "job_id", j.ID, "error", err)
		writeError(w, h.logger, http.StatusInternalServerError, "download failed")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", j.ExportType.ContentType())
	w.Header().Set("Content-Disposition", contentDisposition(j.ArchiveName))

	if _, err := io.Copy(w, f); err != nil {
		h.logger.Error("failed to stream archive", "job_id", j.ID, "error", err)
	}
}

// contentDisposition builds a Content-Disposition header value that carries
// a plain ASCII fallback filename plus an RFC 5987 filename* parameter, so
// non-ASCII/Unicode archive names survive round-tripping
// through browsers that only understand one form or the other.
func contentDisposition(name string) string {
	fallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if fallback == "" {
		fallback = "archive.zip"
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, fallback, url.PathEscape(name))
}
