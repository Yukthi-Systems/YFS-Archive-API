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

// Package service contains the archive service's business logic: request
// validation, manifest building, and orchestration of the ZIP generation
// pipeline. It depends on the repository, storage, client/archive, job and
// token packages but is itself framework-agnostic (no HTTP types).
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/archive"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/config"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/job"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/repository"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/storage"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/token"
)

var (
	// ErrValidation indicates the request failed input validation; safe to
	// surface to the client as a 400.
	ErrValidation = errors.New("validation error")
	// ErrFolderNotFound indicates the root folder does not exist; safe to
	// surface to the client as a 404.
	ErrFolderNotFound = errors.New("root folder not found")
	// ErrQueueFull indicates the archive job queue is at capacity; safe to
	// surface to the client as a 503.
	ErrQueueFull = errors.New("archive service is busy, try again shortly")
	// ErrNotAccepting indicates the service is shutting down and is no
	// longer accepting new archive jobs.
	ErrNotAccepting = errors.New("archive service is shutting down")
)

var archiveNamePattern = regexp.MustCompile(`^[^/\\\x00]{1,255}$`)

// ArchiveService implements the archive request/job lifecycle:
// validating requests, querying PostgreSQL for the folder/file
// manifest, generating the ZIP, and tracking job progress.
type ArchiveService struct {
	repo      repository.ArchiveRepository
	jobs      job.Store
	files     storage.ArchiveStorage
	tokens    token.Service
	fetcher   archive.FileFetcher
	generator *archive.Generator
	cfg       config.ArchiveConfig
	logger    *slog.Logger

	queue     chan string
	accepting atomic.Bool
}

// New builds an ArchiveService. The returned service owns a bounded job
// queue sized by cfg.QueueSize; callers pull jobs from it via Queue().
func New(
	repo repository.ArchiveRepository,
	jobs job.Store,
	files storage.ArchiveStorage,
	tokens token.Service,
	fetcher archive.FileFetcher,
	cfg config.ArchiveConfig,
	logger *slog.Logger,
) *ArchiveService {
	s := &ArchiveService{
		repo:      repo,
		jobs:      jobs,
		files:     files,
		tokens:    tokens,
		fetcher:   fetcher,
		generator: archive.NewGenerator(fetcher, cfg.MaxSizeBytes),
		cfg:       cfg,
		logger:    logger,
		queue:     make(chan string, cfg.QueueSize),
	}
	s.accepting.Store(true)
	return s
}

// Queue exposes the job queue for the worker pool to consume from.
func (s *ArchiveService) Queue() <-chan string {
	return s.queue
}

// StopAccepting marks the service as no longer accepting new archive
// requests. Called at the start of graceful shutdown.
func (s *ArchiveService) StopAccepting() {
	s.accepting.Store(false)
}

// CreateJob validates req, verifies the root folder exists, creates a
// queued job record, and enqueues it for a worker to process. It performs
// only lightweight validation + a folder-existence check inline; the
// expensive folder/file query happens later in ProcessJob.
func (s *ArchiveService) CreateJob(ctx context.Context, req model.ArchiveRequest) (*model.Job, error) {
	if !s.accepting.Load() {
		return nil, ErrNotAccepting
	}

	rootFolderID, err := validateRootFolderID(req.RootFolderID)
	if err != nil {
		return nil, err
	}

	archiveName, err := validateArchiveName(req.ArchiveName)
	if err != nil {
		return nil, err
	}

	exportType, err := validateExportType(req.ExportType)
	if err != nil {
		return nil, err
	}

	exists, err := s.repo.FolderExists(ctx, rootFolderID)
	if err != nil {
		s.logger.Error("failed to verify root folder", "root_folder_id", rootFolderID, "error", err)
		return nil, fmt.Errorf("verify root folder: %w", err)
	}
	if !exists {
		return nil, ErrFolderNotFound
	}

	now := time.Now().UTC()
	j := &model.Job{
		ID:           uuid.NewString(),
		RootFolderID: rootFolderID,
		ArchiveName:  archiveName,
		ExportType:   exportType,
		Status:       model.JobStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.jobs.Create(ctx, j); err != nil {
		return nil, fmt.Errorf("create job record: %w", err)
	}

	select {
	case s.queue <- j.ID:
	default:
		// Roll back the job record; the client should retry.
		_ = s.jobs.Delete(ctx, j.ID)
		return nil, ErrQueueFull
	}

	s.logger.Info("archive job queued", "job_id", j.ID, "root_folder_id", rootFolderID)

	return j, nil
}

// GetJob returns the current state of a job.
func (s *ArchiveService) GetJob(ctx context.Context, jobID string) (*model.Job, error) {
	return s.jobs.Get(ctx, jobID)
}

// ValidateDownloadToken checks tok against jobID.
func (s *ArchiveService) ValidateDownloadToken(ctx context.Context, jobID, tok string) error {
	return s.tokens.Validate(ctx, jobID, tok)
}

// OpenArchive opens the finalized archive file for jobID for streaming.
func (s *ArchiveService) OpenArchive(jobID string) (io.ReadSeekCloser, error) {
	return s.files.Open(jobID)
}

// ProcessJob runs the full archive generation pipeline for jobID. It is
// invoked by a worker goroutine after dequeuing the job ID; ctx should be a
// long-lived context (not tied to a single HTTP request or to server
// shutdown) so an in-flight job can finish during graceful shutdown.
func (s *ArchiveService) ProcessJob(ctx context.Context, jobID string) {
	j, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		s.logger.Error("job disappeared before processing", "job_id", jobID, "error", err)
		return
	}

	j.StartedAt = time.Now().UTC()
	s.logger.Info("archive job started",
		"job_id", j.ID,
		"root_folder_id", j.RootFolderID,
		"queue_wait", j.StartedAt.Sub(j.CreatedAt).String(),
	)

	j.Status = model.JobStatusProcessing
	j.UpdatedAt = j.StartedAt
	if err := s.jobs.Update(ctx, j); err != nil {
		s.logger.Error("failed to mark job processing", "job_id", j.ID, "error", err)
		return
	}

	manifest, err := s.buildManifest(ctx, j)
	if err != nil {
		s.failJob(ctx, j, "failed to read archive manifest from database", err)
		return
	}

	j.TotalFiles = manifest.TotalFiles
	j.TotalBytes = manifest.TotalBytes
	j.UpdatedAt = time.Now().UTC()
	if err := s.jobs.Update(ctx, j); err != nil {
		s.logger.Error("failed to record manifest totals", "job_id", j.ID, "error", err)
		return
	}

	if manifest.TotalBytes > s.cfg.MaxSizeBytes {
		s.failJob(ctx, j, "archive exceeds maximum allowed size",
			fmt.Errorf("total bytes %d exceeds limit %d", manifest.TotalBytes, s.cfg.MaxSizeBytes))
		return
	}

	if err := s.generateArchive(ctx, j, manifest); err != nil {
		s.failJob(ctx, j, "failed to generate archive", err)
		return
	}

	s.completeJob(ctx, j)
}

// buildManifest queries the folder tree and its files for j's root
// folder and assembles them into an ArchiveManifest with totals and the
// set of empty folders.
func (s *ArchiveService) buildManifest(ctx context.Context, j *model.Job) (*model.ArchiveManifest, error) {
	folders, err := s.repo.GetFolderTree(ctx, j.RootFolderID)
	if err != nil {
		return nil, fmt.Errorf("get folder tree: %w", err)
	}

	files, err := s.repo.GetFilesForFolderTree(ctx, j.RootFolderID)
	if err != nil {
		return nil, fmt.Errorf("get files for folder tree: %w", err)
	}

	// The repository roots every path at the folder's own name; the
	// extracted archive should instead be rooted at the name the user
	// chose (e.g. "Documents.zip" -> "Documents/...").
	root := archiveRootDir(j.ArchiveName)
	for i := range folders {
		folders[i].ArchivePath = rebaseArchivePath(folders[i].ArchivePath, root)
	}
	for i := range files {
		files[i].ArchivePath = rebaseArchivePath(files[i].ArchivePath, root)
	}

	var totalBytes int64
	for _, f := range files {
		totalBytes += f.FileSize
	}

	return &model.ArchiveManifest{
		RootFolderID: j.RootFolderID,
		ArchiveName:  j.ArchiveName,
		ExportType:   j.ExportType,
		Files:        files,
		EmptyFolders: computeEmptyFolders(folders, files),
		TotalFiles:   int64(len(files)),
		TotalBytes:   totalBytes,
	}, nil
}

// generateArchive streams manifest into a temporary archive, recording
// progress on j as each file completes, and atomically publishes the
// result. On any error the partial archive is deleted.
func (s *ArchiveService) generateArchive(ctx context.Context, j *model.Job, manifest *model.ArchiveManifest) error {
	w, err := s.files.Create(j.ID)
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}

	// Persist progress on every file completion (cheap against the
	// in-memory store); the SSE handler throttles outbound network events
	// to one poll every 500ms itself.
	onProgress := func(processedFiles, processedBytes int64) {
		j.ProcessedFiles = processedFiles
		j.ProcessedBytes = processedBytes
		j.UpdatedAt = time.Now().UTC()
		if err := s.jobs.Update(ctx, j); err != nil {
			s.logger.Warn("failed to persist progress", "job_id", j.ID, "error", err)
		}
	}

	genErr := s.generator.Generate(ctx, w, manifest, onProgress)
	closeErr := w.Close()

	if genErr != nil {
		_ = s.files.Delete(j.ID)
		return genErr
	}
	if closeErr != nil {
		_ = s.files.Delete(j.ID)
		return fmt.Errorf("close temp archive: %w", closeErr)
	}

	if err := s.files.RenameTemp(j.ID); err != nil {
		_ = s.files.Delete(j.ID)
		return fmt.Errorf("finalize archive: %w", err)
	}

	return nil
}

// completeJob issues a download token for j and marks it completed with
// an expiry of cfg.Expiry from now.
func (s *ArchiveService) completeJob(ctx context.Context, j *model.Job) {
	now := time.Now().UTC()

	tok, err := s.tokens.Issue(ctx, j.ID, s.cfg.Expiry)
	if err != nil {
		s.failJob(ctx, j, "failed to issue download token", err)
		return
	}

	j.Status = model.JobStatusCompleted
	j.ArchivePath = j.ID + "." + j.ExportType.Extension()
	j.DownloadToken = tok
	j.CompletedAt = &now
	j.ExpiresAt = now.Add(s.cfg.Expiry)
	j.UpdatedAt = now

	if err := s.jobs.Update(ctx, j); err != nil {
		s.logger.Error("failed to persist job completion", "job_id", j.ID, "error", err)
		return
	}

	s.logger.Info("archive job completed",
		"job_id", j.ID,
		"total_files", j.TotalFiles,
		"total_bytes", j.TotalBytes,
		"duration", now.Sub(j.StartedAt).String(),
		"duration_ms", now.Sub(j.StartedAt).Milliseconds(),
		"total_duration", now.Sub(j.CreatedAt).String(),
	)
}

// failJob logs cause and marks j failed. Only safeMessage is stored on
// the job (and therefore shown to clients); cause stays in the logs.
func (s *ArchiveService) failJob(ctx context.Context, j *model.Job, safeMessage string, cause error) {
	now := time.Now().UTC()
	s.logger.Error("archive job failed",
		"job_id", j.ID,
		"root_folder_id", j.RootFolderID,
		"error", cause,
		"duration", now.Sub(j.StartedAt).String(),
		"duration_ms", now.Sub(j.StartedAt).Milliseconds(),
	)

	j.Status = model.JobStatusFailed
	j.ErrorMessage = safeMessage
	j.UpdatedAt = now

	if err := s.jobs.Update(ctx, j); err != nil {
		s.logger.Error("failed to persist job failure", "job_id", j.ID, "error", err)
	}
}

// validateRootFolderID requires raw to be a UUID and returns it in
// canonical form.
func validateRootFolderID(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: root_folder_id is required", ErrValidation)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: root_folder_id is not a valid UUID", ErrValidation)
	}
	return id.String(), nil
}

// validateArchiveName trims raw and requires 1-255 characters with no
// path separators or NUL bytes, rejecting "." and "..".
func validateArchiveName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: archive_name is required", ErrValidation)
	}
	if !archiveNamePattern.MatchString(name) {
		return "", fmt.Errorf("%w: archive_name must be 1-255 characters and contain no path separators", ErrValidation)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("%w: archive_name is invalid", ErrValidation)
	}
	return name, nil
}

// archiveRootDir derives the archive's top-level directory name from the
// user-supplied archive name by stripping a trailing ".zip" or ".tar"
// (case-insensitive). It returns "" if nothing usable remains.
func archiveRootDir(archiveName string) string {
	name := strings.TrimSpace(archiveName)
	lower := strings.ToLower(name)
	for _, ext := range []string{".zip", ".tar"} {
		if strings.HasSuffix(lower, ext) {
			name = strings.TrimSpace(name[:len(name)-len(ext)])
			break
		}
	}
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// rebaseArchivePath replaces the first segment of p (the root folder's
// name) with root. An empty root leaves p unchanged.
func rebaseArchivePath(p, root string) string {
	if root == "" {
		return p
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return root + p[i:]
	}
	return root
}

// validateExportType defaults an empty export_type to ZIP (for backward
// compatibility with callers that predate the field) and otherwise requires
// it to be one of the supported container formats.
func validateExportType(raw model.ExportType) (model.ExportType, error) {
	switch raw {
	case "":
		return model.ExportTypeZip, nil
	case model.ExportTypeZip, model.ExportTypeTar:
		return raw, nil
	default:
		return "", fmt.Errorf("%w: export_type must be \"zip\" or \"tar\"", ErrValidation)
	}
}

// computeEmptyFolders returns the folders whose entire subtree (the folder
// itself plus all descendants) contains no files. Those folders need an
// explicit directory entry in the ZIP, because no file path will otherwise
// cause an archive tool to materialize them.
func computeEmptyFolders(folders []model.ArchiveFolder, files []model.ArchiveFile) []model.ArchiveFolder {
	if len(folders) == 0 {
		return nil
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.ArchivePath
	}
	sort.Strings(paths)

	hasFileInSubtree := func(prefix string) bool {
		target := prefix + "/"
		idx := sort.SearchStrings(paths, target)
		return idx < len(paths) && strings.HasPrefix(paths[idx], target)
	}

	var empty []model.ArchiveFolder
	for _, folder := range folders {
		if !hasFileInSubtree(folder.ArchivePath) {
			empty = append(empty, folder)
		}
	}
	return empty
}
