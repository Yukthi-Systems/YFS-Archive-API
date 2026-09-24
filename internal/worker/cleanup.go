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

package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/job"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/storage"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/token"
)

// Cleanup periodically removes expired archive jobs: their finalized ZIP,
// any stray .tmp file, their job record, and their download tokens. It also
// reconciles files on disk with no matching job record, so a stale .tmp or
// orphaned .zip left behind by a crash or restart is eventually removed
// (the in-memory JobStore starts empty on every restart, so this is also
// what reclaims disk space for archives that finished in a previous
// process's lifetime).
type Cleanup struct {
	jobs        job.Store
	files       storage.ArchiveStorage
	tokens      token.Service
	interval    time.Duration
	ttlFallback time.Duration
	logger      *slog.Logger
}

// NewCleanup builds a Cleanup worker. ttlFallback is used as the effective
// TTL for jobs that have no ExpiresAt set yet (still queued/processing) or
// whose ExpiresAt was never recorded, measured from CreatedAt.
func NewCleanup(jobs job.Store, files storage.ArchiveStorage, tokens token.Service, interval, ttlFallback time.Duration, logger *slog.Logger) *Cleanup {
	return &Cleanup{
		jobs:        jobs,
		files:       files,
		tokens:      tokens,
		interval:    interval,
		ttlFallback: ttlFallback,
		logger:      logger,
	}
}

// Run blocks, sweeping every interval, until ctx is done.
func (c *Cleanup) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.sweep(ctx)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("cleanup worker stopping")
			return
		case <-ticker.C:
			c.sweep(ctx)
		}
	}
}

// sweep removes expired jobs, then deletes any archive file on disk that
// has no matching job record.
func (c *Cleanup) sweep(ctx context.Context) {
	jobs, err := c.jobs.List(ctx)
	if err != nil {
		c.logger.Error("cleanup: failed to list jobs", "error", err)
		return
	}

	now := time.Now()
	tracked := make(map[string]struct{}, len(jobs))

	for _, j := range jobs {
		if c.isExpired(j, now) {
			c.removeJob(ctx, j.ID)
			continue
		}
		tracked[j.ID] = struct{}{}
	}

	ids, err := c.files.ListJobIDs()
	if err != nil {
		c.logger.Error("cleanup: failed to list archive files", "error", err)
		return
	}
	for _, id := range ids {
		if _, ok := tracked[id]; ok {
			continue
		}
		if err := c.files.Delete(id); err != nil {
			c.logger.Error("cleanup: failed to remove orphaned archive", "job_id", id, "error", err)
			continue
		}
		c.logger.Info("cleanup: removed orphaned archive file", "job_id", id)
	}
}

// isExpired reports whether j has passed its ExpiresAt, or, if unset,
// CreatedAt plus the fallback TTL.
func (c *Cleanup) isExpired(j *model.Job, now time.Time) bool {
	deadline := j.ExpiresAt
	if deadline.IsZero() {
		deadline = j.CreatedAt.Add(c.ttlFallback)
	}
	return now.After(deadline)
}

// removeJob deletes a job's archive files, revokes its download tokens
// and removes its job record, logging (not returning) any failures.
func (c *Cleanup) removeJob(ctx context.Context, jobID string) {
	if err := c.files.Delete(jobID); err != nil {
		c.logger.Error("cleanup: failed to delete archive", "job_id", jobID, "error", err)
	}
	if err := c.tokens.Revoke(ctx, jobID); err != nil {
		c.logger.Error("cleanup: failed to revoke tokens", "job_id", jobID, "error", err)
	}
	if err := c.jobs.Delete(ctx, jobID); err != nil {
		c.logger.Error("cleanup: failed to delete job record", "job_id", jobID, "error", err)
		return
	}
	c.logger.Info("cleanup: removed expired archive job", "job_id", jobID)
}
