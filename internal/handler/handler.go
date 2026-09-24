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

// Package handler contains HTTP handlers. Handlers parse/validate the HTTP
// layer only and delegate all business logic to internal/service; no SQL or
// storage I/O happens here directly.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON writes body as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("failed to write json response", "error", err)
	}
}

// errorBody is the JSON shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}

// writeError writes a safe, generic error message to the client. Detailed
// error information belongs in the server log, not the HTTP response.
func writeError(w http.ResponseWriter, logger *slog.Logger, status int, safeMessage string) {
	writeJSON(w, logger, status, errorBody{Error: safeMessage})
}
