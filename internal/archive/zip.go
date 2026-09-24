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

// Package archive builds streaming ZIP archives from an archive manifest,
// downloading each file's bytes from its storage server and writing them
// directly into the ZIP writer without buffering full files in memory or on
// disk.
package archive

import (
	"archive/tar"
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/client"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
)

// ProgressFunc is invoked after each file is written into the archive (or
// fails), with the cumulative processed file/byte counts.
type ProgressFunc func(processedFiles, processedBytes int64)

// FileFetcher resolves and downloads a single archive file's content. It is
// satisfied by a StorageClient composed on its own; kept as its own small
// interface here so Generator does not depend on the client package's
// config wiring.
type FileFetcher interface {
	Fetch(ctx context.Context, f model.ArchiveFile) (io.ReadCloser, error)
}

// StorageFetcher is the production FileFetcher: it downloads a file's
// content from the server named by its HostedAt (file_versions.hosted_at)
// via a StorageClient, at its FileLocation path.
type StorageFetcher struct {
	Client client.StorageClient
}

// Fetch downloads af's content from its storage server. The caller must
// close the returned reader.
func (f *StorageFetcher) Fetch(ctx context.Context, af model.ArchiveFile) (io.ReadCloser, error) {
	resp, err := f.Client.Get(ctx, af.HostedAt, af.FileLocation)
	if err != nil {
		return nil, err
	}
	return &responseBody{resp: resp}, nil
}

// responseBody adapts an *http.Response so closing it closes the body.
type responseBody struct {
	resp *http.Response
}

// Read reads from the underlying response body.
func (r *responseBody) Read(p []byte) (int, error) { return r.resp.Body.Read(p) }

// Close closes the underlying response body.
func (r *responseBody) Close() error { return r.resp.Body.Close() }

// ErrSizeLimitExceeded is returned when the bytes actually written into the
// archive exceed maxSizeBytes, even though the pre-computed manifest total
// was within the limit (e.g. a storage server reporting a smaller size than
// it actually serves).
type ErrSizeLimitExceeded struct {
	MaxSizeBytes int64
}

// Error implements the error interface.
func (e *ErrSizeLimitExceeded) Error() string {
	return fmt.Sprintf("archive exceeded maximum size of %d bytes", e.MaxSizeBytes)
}

// Generator streams a manifest into a ZIP archive.
type Generator struct {
	Fetcher      FileFetcher
	MaxSizeBytes int64
	// copyBufSize sizes the buffer used for io.CopyBuffer between the
	// storage response and the zip entry writer.
	copyBufSize int
}

// NewGenerator builds a Generator. maxSizeBytes bounds the total bytes
// actually written into the archive (defense in depth beyond the
// pre-generation manifest size check).
func NewGenerator(fetcher FileFetcher, maxSizeBytes int64) *Generator {
	return &Generator{Fetcher: fetcher, MaxSizeBytes: maxSizeBytes, copyBufSize: 1 << 20}
}

// archiveWriter abstracts the container format (ZIP or TAR) so Generate's
// file-copy loop doesn't need to know which one it's writing. Both
// implementations write directly to the underlying io.Writer with no
// full-file buffering.
type archiveWriter interface {
	// CreateFile begins a new file entry named path, declaring size bytes
	// of content will follow, and returns a writer for that content.
	CreateFile(path string, size int64) (io.Writer, error)
	// CreateDir writes an explicit directory entry for path.
	CreateDir(path string) error
	// Close finalizes the archive (writing any trailing format-specific
	// data) and must be called exactly once, after all entries are written.
	Close() error
}

// newArchiveWriter returns the archiveWriter for exportType, defaulting
// to ZIP for an empty or unrecognized value.
func newArchiveWriter(w io.Writer, exportType model.ExportType) archiveWriter {
	if exportType == model.ExportTypeTar {
		return &tarArchiveWriter{tw: tar.NewWriter(w)}
	}
	return &zipArchiveWriter{zw: zip.NewWriter(w)}
}

// zipArchiveWriter writes a Deflate-compressed ZIP archive.
type zipArchiveWriter struct{ zw *zip.Writer }

// CreateFile starts a Deflate-compressed entry. ZIP streams entry sizes
// in a trailing data descriptor, so the declared size is unused.
func (a *zipArchiveWriter) CreateFile(path string, _ int64) (io.Writer, error) {
	return a.zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate})
}

// CreateDir writes an explicit directory entry (a name ending in "/").
func (a *zipArchiveWriter) CreateDir(path string) error {
	_, err := a.zw.Create(path + "/")
	return err
}

// Close writes the ZIP central directory.
func (a *zipArchiveWriter) Close() error { return a.zw.Close() }

// tarArchiveWriter writes a plain (uncompressed) TAR archive. Unlike ZIP,
// TAR embeds each entry's exact size in its header up front (no streaming
// data descriptor), so CreateFile declares size from the manifest's
// (database-sourced) file_size; a mismatch against what's actually streamed
// surfaces as a write error from the tar package itself.
type tarArchiveWriter struct{ tw *tar.Writer }

// CreateFile writes a regular-file header declaring size bytes and
// returns the tar writer for the entry's content.
func (a *tarArchiveWriter) CreateFile(path string, size int64) (io.Writer, error) {
	if err := a.tw.WriteHeader(&tar.Header{
		Name:     path,
		Size:     size,
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, err
	}
	return a.tw, nil
}

// CreateDir writes a directory header for path.
func (a *tarArchiveWriter) CreateDir(path string) error {
	return a.tw.WriteHeader(&tar.Header{
		Name:     path + "/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	})
}

// Close writes the TAR end-of-archive trailer.
func (a *tarArchiveWriter) Close() error { return a.tw.Close() }

// Generate streams manifest into w as a ZIP or TAR archive (per
// manifest.ExportType; empty defaults to ZIP), invoking onProgress after
// each file. It never buffers a full file in memory: bytes flow storage
// response -> io.CopyBuffer -> archiveWriter -> w.
//
// Directory entries are written for every folder in manifest.EmptyFolders
// (folders whose entire subtree contains no files) so empty directory
// structure survives round-tripping through the archive.
func (g *Generator) Generate(ctx context.Context, w io.Writer, manifest *model.ArchiveManifest, onProgress ProgressFunc) error {
	aw := newArchiveWriter(w, manifest.ExportType)

	var processedBytes int64
	var processedFiles int64

	for _, f := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return err
		}

		archivePath, err := sanitizeArchivePath(f.ArchivePath)
		if err != nil {
			return fmt.Errorf("file %s: %w", f.FileID, err)
		}

		if processedBytes+f.FileSize > g.MaxSizeBytes {
			return &ErrSizeLimitExceeded{MaxSizeBytes: g.MaxSizeBytes}
		}

		if err := g.writeFile(ctx, aw, archivePath, f, &processedBytes); err != nil {
			return fmt.Errorf("write file %s (%s): %w", f.FileID, f.ArchivePath, err)
		}

		processedFiles++
		if onProgress != nil {
			onProgress(processedFiles, processedBytes)
		}
	}

	for _, dir := range manifest.EmptyFolders {
		dirPath, err := sanitizeArchivePath(dir.ArchivePath)
		if err != nil {
			return fmt.Errorf("folder %s: %w", dir.FolderID, err)
		}
		if err := aw.CreateDir(dirPath); err != nil {
			return fmt.Errorf("create directory entry %s: %w", dirPath, err)
		}
	}

	if err := aw.Close(); err != nil {
		return fmt.Errorf("close archive writer: %w", err)
	}

	return nil
}

// writeFile fetches a single file from storage and streams it into a new
// archive entry, adding the bytes written to *processedBytes. It fails
// with ErrSizeLimitExceeded if the running total passes MaxSizeBytes.
func (g *Generator) writeFile(ctx context.Context, aw archiveWriter, archivePath string, f model.ArchiveFile, processedBytes *int64) error {
	body, err := g.Fetcher.Fetch(ctx, f)
	if err != nil {
		return fmt.Errorf("fetch from storage: %w", err)
	}
	defer body.Close()

	entry, err := aw.CreateFile(archivePath, f.FileSize)
	if err != nil {
		return fmt.Errorf("create archive entry: %w", err)
	}

	buf := make([]byte, g.copyBufSize)
	limited := io.LimitReader(body, g.MaxSizeBytes-*processedBytes+1)
	n, err := io.CopyBuffer(entry, limited, buf)
	if err != nil {
		return fmt.Errorf("stream file content: %w", err)
	}
	*processedBytes += n

	if *processedBytes > g.MaxSizeBytes {
		return &ErrSizeLimitExceeded{MaxSizeBytes: g.MaxSizeBytes}
	}

	return nil
}

// sanitizeArchivePath validates and cleans an archive path so it can never
// escape the archive root: no absolute paths, no ".." traversal, no drive
// letters, and no empty segments. It returns the cleaned, forward-slash,
// relative path.
func sanitizeArchivePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty archive path")
	}

	normalized := strings.ReplaceAll(p, "\\", "/")

	if strings.Contains(normalized, "\x00") {
		return "", fmt.Errorf("invalid archive path %q: contains null byte", p)
	}

	// Reject Windows drive letters (e.g. "C:").
	if len(normalized) >= 2 && normalized[1] == ':' {
		return "", fmt.Errorf("invalid archive path %q: contains drive letter", p)
	}

	cleaned := path.Clean(normalized)
	cleaned = strings.TrimPrefix(cleaned, "/")

	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("invalid archive path %q: escapes archive root", p)
	}

	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid archive path %q: invalid segment %q", p, segment)
		}
	}

	return cleaned, nil
}
