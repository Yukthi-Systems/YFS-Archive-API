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

// Command server is the entry point for the archive service. It only
// orchestrates startup and shutdown; all behavior lives in internal/.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/archive"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/client"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/config"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/job"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/logger"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/repository"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/server"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/service"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/storage"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/token"
	"github.com/Yukthi-Systems/YFS-Archive-API/internal/worker"
)

// main runs the service and exits with a non-zero status if startup
// fails or the HTTP server stops unexpectedly.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// run wires every dependency together, starts the worker pool, cleanup
// sweep and HTTP server, then blocks until a shutdown signal arrives or
// the server fails. It returns an error only for startup or server
// failures; a signal-triggered shutdown returns nil.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, logCloser, err := logger.New(cfg.Log)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logCloser.Close()

	log.Info("starting archive service", "env", cfg.App.Env)

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbPool, err := repository.NewPool(rootCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer dbPool.Close()
	log.Info("connected to database")

	archiveStorage, err := storage.NewLocalArchiveStorage(cfg.Archive.TempDir)
	if err != nil {
		return fmt.Errorf("init archive storage: %w", err)
	}

	storageServerRepo := repository.NewStorageServerRepository(dbPool)
	storageServers, err := storageServerRepo.ListServers(rootCtx)
	if err != nil {
		return fmt.Errorf("load storage servers: %w", err)
	}
	if len(storageServers) == 0 {
		log.Warn("no storage servers found; archive jobs referencing file data will fail")
	}
	servers := make(map[string]client.ServerConfig, len(storageServers))
	for _, s := range storageServers {
		servers[s.HostAddress] = client.ServerConfig{BaseURL: s.HostAddress, APIKey: s.APIKey}
	}
	log.Info("loaded storage servers from database", "count", len(servers))

	storageClient := client.NewHTTPStorageClient(servers, cfg.Storage.RequestTimeout)
	fetcher := &archive.StorageFetcher{Client: storageClient}

	repo := repository.NewArchiveRepository(dbPool)
	jobStore := job.NewMemoryStore()
	tokenService := token.NewInMemoryService()

	archiveService := service.New(repo, jobStore, archiveStorage, tokenService, fetcher, cfg.Archive, log)

	// shutdownCtx is cancelled the moment a shutdown signal is received; it
	// governs whether SSE handlers keep streaming and whether the worker
	// pool keeps pulling *new* jobs off the queue. It is distinct from
	// rootCtx (used for one-shot startup calls) and from the
	// context.Background() each worker uses while actively processing a
	// job, so in-flight jobs are allowed to finish during shutdown.
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	defer cancelShutdown()

	workerPool := worker.NewPool(archiveService, cfg.Archive.Workers, log)
	workerPool.Run(shutdownCtx)

	cleanup := worker.NewCleanup(jobStore, archiveStorage, tokenService, cfg.Archive.CleanupInterval, cfg.Archive.Expiry, log)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		cleanup.Run(shutdownCtx)
	}()

	httpServer := server.New(cfg.Server, archiveService, cfg.Archive.Expiry, shutdownCtx, log)

	listener, err := net.Listen("tcp", cfg.Server.Addr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Server.Addr(), err)
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
			return
		}
		serverErrCh <- nil
	}()

	log.Info("http server listening", "addr", cfg.Server.Addr())

	select {
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	case err := <-serverErrCh:
		if err != nil {
			log.Error("http server failed", "error", err)
		}
		return err
	}

	return shutdown(cfg, httpServer, archiveService, workerPool, cancelShutdown, cleanupDone, log)
}

// shutdown performs the ordered graceful-shutdown sequence: stop
// accepting jobs, stop workers from dequeuing, wait for
// in-flight jobs (bounded by cfg.Server.ShutdownTimeout), then close the
// HTTP server. It always returns nil; timeouts are logged, not returned.
func shutdown(
	cfg *config.Config,
	httpServer *http.Server,
	archiveService *service.ArchiveService,
	workerPool *worker.Pool,
	cancelShutdown context.CancelFunc,
	cleanupDone <-chan struct{},
	log *slog.Logger,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// Stop accepting new archive job requests.
	archiveService.StopAccepting()

	// Stop the worker pool from pulling any *new* jobs off the queue and
	// signal the cleanup worker and SSE handlers to stop; jobs already
	// in-flight are unaffected since they run on context.Background().
	cancelShutdown()

	// Allow active jobs to finish, bounded by the shutdown timeout.
	workersDone := make(chan struct{})
	go func() {
		workerPool.Wait()
		close(workersDone)
	}()

	select {
	case <-workersDone:
		log.Info("all workers stopped")
	case <-ctx.Done():
		log.Warn("shutdown timeout exceeded before all workers stopped")
	}

	select {
	case <-cleanupDone:
	case <-ctx.Done():
	}

	// Shut down the HTTP server (closes SSE connections and stops
	// accepting new connections/requests).
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error("http server shutdown error", "error", err)
	}

	log.Info("shutdown complete")
	return nil
}
