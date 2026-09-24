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

// Package storage defines where generated archive ZIP files live on disk
// (or, in the future, in object storage) while they are being built and
// while they are available for download.
package storage

import "io"

// ArchiveStorage abstracts temporary archive persistence so the archive
// generator (internal/archive) and download handler never touch the
// filesystem directly. A LocalArchiveStorage implementation is provided for
// the first version; this interface allows a future S3/MinIO-backed
// implementation to be dropped in without touching callers.
type ArchiveStorage interface {
	// Create opens a new temporary write target for jobID (the
	// "<job-id>.zip.tmp" file). The caller must Close it; on success it
	// should then call RenameTemp to publish it.
	Create(jobID string) (io.WriteCloser, error)

	// Open opens the finalized archive for jobID (the "<job-id>.zip" file)
	// for reading, e.g. for streaming a download.
	Open(jobID string) (io.ReadCloser, error)

	// Delete removes both the finalized archive and any leftover temp file
	// for jobID. It must not error if neither exists.
	Delete(jobID string) error

	// RenameTemp atomically publishes the temp file written via Create as
	// the finalized archive for jobID. Only after this succeeds is the
	// archive safe to expose to download clients.
	RenameTemp(jobID string) error

	// Size returns the size in bytes of the finalized archive for jobID.
	Size(jobID string) (int64, error)

	// ListJobIDs returns the job IDs of all finalized and temporary
	// archives currently on disk, for cleanup-worker reconciliation after a
	// restart.
	ListJobIDs() ([]string, error)
}
