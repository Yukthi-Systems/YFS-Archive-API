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

package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
)

// StorageServerRepository provides read access to the servers table.
// Storage server identity/base URL (host_address) and API key (secret_key)
// live in the database (rather than the process environment) so servers can
// be added or rotated without a config change or redeploy of this service.
//
// Like ArchiveRepository, this only ever issues SELECT statements: this
// service is expected to connect with a database role granted SELECT-only
// privileges (see the README's "Database schema" section for the tables and
// columns it expects; this service never creates or migrates schema).
type StorageServerRepository interface {
	// ListServers returns every known storage server.
	ListServers(ctx context.Context) ([]model.StorageServer, error)
}

// pgStorageServerRepository is the pgxpool-backed
// StorageServerRepository.
type pgStorageServerRepository struct {
	pool *pgxpool.Pool
}

// NewStorageServerRepository builds a StorageServerRepository backed by pool.
func NewStorageServerRepository(pool *pgxpool.Pool) StorageServerRepository {
	return &pgStorageServerRepository{pool: pool}
}

const listStorageServersQuery = `
SELECT host_address, secret_key
FROM servers
ORDER BY host_address`

// ListServers implements StorageServerRepository.ListServers.
func (r *pgStorageServerRepository) ListServers(ctx context.Context) ([]model.StorageServer, error) {
	rows, err := r.pool.Query(ctx, listStorageServersQuery)
	if err != nil {
		return nil, fmt.Errorf("query storage servers: %w", err)
	}
	defer rows.Close()

	var servers []model.StorageServer
	for rows.Next() {
		var s model.StorageServer
		if err := rows.Scan(&s.HostAddress, &s.APIKey); err != nil {
			return nil, fmt.Errorf("scan storage server row: %w", err)
		}
		servers = append(servers, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage server rows: %w", err)
	}

	return servers, nil
}
