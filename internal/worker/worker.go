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

// Package worker runs the archive job worker pool and the background
// cleanup sweep.
package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/service"
)

// Pool runs a fixed number of goroutines that pull job IDs off the
// ArchiveService's queue and process them. Processing itself always runs
// against context.Background() (not the pool's run context) so that an
// in-flight job finishes even after graceful shutdown begins; Run's ctx
// only governs whether a worker picks up another job from the queue.
type Pool struct {
	svc     *service.ArchiveService
	workers int
	logger  *slog.Logger

	wg sync.WaitGroup
}

// NewPool builds a worker pool of size workers over svc.
func NewPool(svc *service.ArchiveService, workers int, logger *slog.Logger) *Pool {
	return &Pool{svc: svc, workers: workers, logger: logger}
}

// Run starts the worker goroutines. It returns immediately; call Wait to
// block until all workers have exited (which happens once ctx is done and
// each worker's current job, if any, has finished).
func (p *Pool) Run(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		workerID := i
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.loop(ctx, workerID)
		}()
	}
}

// loop is a single worker goroutine: it processes queued jobs one at a
// time until ctx is done.
func (p *Pool) loop(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("worker stopping", "worker_id", workerID)
			return
		case jobID, ok := <-p.svc.Queue():
			if !ok {
				return
			}
			p.svc.ProcessJob(context.Background(), jobID)
		}
	}
}

// Wait blocks until all worker goroutines have exited.
func (p *Pool) Wait() {
	p.wg.Wait()
}
