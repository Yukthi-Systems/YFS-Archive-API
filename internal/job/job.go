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

// Package job defines archive job persistence.
package job

import (
	"context"
	"errors"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
)

// ErrNotFound is returned when a job ID has no matching job.
var ErrNotFound = errors.New("job not found")

// Store persists archive job state. The first implementation
// (MemoryStore) is process-local; the interface is kept independent of that
// choice so a Redis-backed implementation can replace it for horizontally
// scaled deployments without touching callers. Do not design the rest of
// the application around Redis.
type Store interface {
	Create(ctx context.Context, job *model.Job) error
	Get(ctx context.Context, jobID string) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) error
	Delete(ctx context.Context, jobID string) error
	// List returns all jobs currently in the store, for cleanup-worker
	// sweeps.
	List(ctx context.Context) ([]*model.Job, error)
}
