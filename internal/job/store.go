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

package job

import (
	"context"
	"fmt"
	"sync"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
)

// MemoryStore is an in-memory, process-local Store implementation.
// See Store's doc comment for the horizontal-scaling caveat.
type MemoryStore struct {
	mu   sync.RWMutex
	jobs map[string]*model.Job
}

// NewMemoryStore creates an empty in-memory job store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{jobs: make(map[string]*model.Job)}
}

// Create implements Store.Create. It fails if a job with the same ID
// already exists.
func (s *MemoryStore) Create(_ context.Context, j *model.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[j.ID]; exists {
		return fmt.Errorf("job %s already exists", j.ID)
	}
	s.jobs[j.ID] = j.Clone()
	return nil
}

// Get implements Store.Get, returning a copy of the stored job.
func (s *MemoryStore) Get(_ context.Context, jobID string) (*model.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j, ok := s.jobs[jobID]
	if !ok {
		return nil, ErrNotFound
	}
	return j.Clone(), nil
}

// Update implements Store.Update, replacing the stored job with a copy
// of j.
func (s *MemoryStore) Update(_ context.Context, j *model.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[j.ID]; !exists {
		return ErrNotFound
	}
	s.jobs[j.ID] = j.Clone()
	return nil
}

// Delete implements Store.Delete. Deleting an unknown job is not an
// error.
func (s *MemoryStore) Delete(_ context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, jobID)
	return nil
}

// List implements Store.List, returning copies of all stored jobs.
func (s *MemoryStore) List(_ context.Context) ([]*model.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]*model.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j.Clone())
	}
	return jobs, nil
}
