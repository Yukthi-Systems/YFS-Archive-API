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

// Package model contains the core domain types shared across the archive
// service layers (repository, service, worker, handler).
package model

import "time"

// ArchiveRequest is the payload accepted by POST /internal/archives.
type ArchiveRequest struct {
	RootFolderID string     `json:"root_folder_id"`
	ArchiveName  string     `json:"archive_name"`
	ExportType   ExportType `json:"export_type"`
}

// ExportType selects the container format used to produce an archive.
type ExportType string

const (
	// ExportTypeZip produces a ZIP archive. It is the default when
	// ExportType is left empty, for backward compatibility with callers
	// that predate export_type.
	ExportTypeZip ExportType = "zip"
	// ExportTypeTar produces a plain (uncompressed) TAR archive. Both ZIP
	// and TAR extract natively on macOS, Linux, and Windows (10+ ships
	// tar.exe; Explorer also opens ZIP natively).
	ExportTypeTar ExportType = "tar"
)

// Extension returns the file extension (without a leading dot) associated
// with t, defaulting to "zip" for an empty/unrecognized value.
func (t ExportType) Extension() string {
	if t == ExportTypeTar {
		return "tar"
	}
	return "zip"
}

// ContentType returns the MIME type associated with t, defaulting to ZIP's
// for an empty/unrecognized value.
func (t ExportType) ContentType() string {
	if t == ExportTypeTar {
		return "application/x-tar"
	}
	return "application/zip"
}

// StorageServer is a single storage server's connection details, resolved
// from the database (the servers table) rather than the process
// environment, since new servers can be added/rotated without a config
// change or redeploy of this service. HostAddress doubles as both the
// server's identity and its base URL (e.g. "https://store-1.files.example");
// there is no separate server ID or base URL column.
type StorageServer struct {
	HostAddress string
	APIKey      string
}

// ArchiveFile represents a single file selected for inclusion in an archive,
// resolved to its latest version and storage location.
type ArchiveFile struct {
	FileID      string
	FileName    string
	ArchivePath string
	FileVersion int
	// HostedAt is the file_versions.hosted_at value: the servers.host_address
	// of the server holding this version's content.
	HostedAt string
	// FileLocation is the path relative to HostedAt (e.g.
	// "/org-id/user-id/ab/file-id"), not a self-contained URL.
	FileLocation string
	FileSize     int64
	FileHash     string
}

// ArchiveFolder represents a folder discovered in the recursive folder tree.
type ArchiveFolder struct {
	FolderID       string
	ParentFolderID *string
	FolderName     string
	ArchivePath    string
}

// ArchiveManifest is the fully resolved set of files and empty folders that
// make up an archive job, built after querying PostgreSQL and before any
// storage I/O begins.
type ArchiveManifest struct {
	RootFolderID string
	ArchiveName  string
	ExportType   ExportType
	Files        []ArchiveFile
	EmptyFolders []ArchiveFolder
	TotalFiles   int64
	TotalBytes   int64
}

// JobStatus enumerates the lifecycle states of an archive job.
type JobStatus string

const (
	JobStatusQueued     JobStatus = "queued"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusExpired    JobStatus = "expired"
)

// Job tracks the state and progress of a single archive generation request.
type Job struct {
	ID             string
	RootFolderID   string
	ArchiveName    string
	ExportType     ExportType
	Status         JobStatus
	TotalFiles     int64
	ProcessedFiles int64
	TotalBytes     int64
	ProcessedBytes int64
	ArchivePath    string
	ErrorMessage   string
	// DownloadToken is set once the job completes successfully. It is
	// server-internal convenience state for building the SSE "completed"
	// event and is never re-derived from client input; the download
	// endpoint still authoritatively validates presented tokens through
	// the token.Service.
	DownloadToken string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// StartedAt is when a worker picked the job up; zero while queued.
	StartedAt   time.Time
	CompletedAt *time.Time
	ExpiresAt   time.Time
}

// Clone returns a deep-enough copy of the job for safe handoff across
// goroutine boundaries (SSE readers, handlers) without shared mutable state.
func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	clone := *j
	if j.CompletedAt != nil {
		completedAt := *j.CompletedAt
		clone.CompletedAt = &completedAt
	}
	return &clone
}

// ProgressEvent is the payload sent on SSE "progress" events.
type ProgressEvent struct {
	ProcessedFiles int64 `json:"processed_files"`
	TotalFiles     int64 `json:"total_files"`
	ProcessedBytes int64 `json:"processed_bytes"`
	TotalBytes     int64 `json:"total_bytes"`
}

// CompletedEvent is the payload sent on SSE "completed" events.
type CompletedEvent struct {
	JobID         string `json:"job_id"`
	DownloadURL   string `json:"download_url"`
	DownloadToken string `json:"download_token"`
	ExpiresInSecs int64  `json:"expires_in"`
}

// ErrorEvent is the payload sent on SSE "error" events.
type ErrorEvent struct {
	Message string `json:"message"`
}
