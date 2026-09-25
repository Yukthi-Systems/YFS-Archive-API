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
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yukthi-Systems/YFS-Archive-API/internal/model"
)

// ErrFolderNotFound is returned when the requested root folder does not
// exist or has been soft-deleted.
var ErrFolderNotFound = errors.New("folder not found")

// ArchiveRepository provides read access to the folder/file/version schema
// needed to build an archive manifest. Implementations must not hold a
// long-running transaction across calls; each method executes and releases
// its own connection back to the pool.
type ArchiveRepository interface {
	// FolderExists reports whether an undeleted folder with the given ID
	// exists.
	FolderExists(ctx context.Context, folderID string) (bool, error)

	// GetFolderTree returns every undeleted folder in the subtree rooted at
	// rootFolderID, including the root folder itself. Paths start with the
	// root folder's name (e.g. "Docs", "Docs/docs1").
	GetFolderTree(ctx context.Context, rootFolderID string) ([]model.ArchiveFolder, error)

	// GetFilesForFolderTree returns every undeleted file within the subtree
	// rooted at rootFolderID (including files directly inside the root),
	// each resolved to its latest file_version, with archive paths starting
	// with the root folder's name (e.g. "Docs/docs1/a.txt").
	GetFilesForFolderTree(ctx context.Context, rootFolderID string) ([]model.ArchiveFile, error)
}

// pgArchiveRepository is the pgxpool-backed ArchiveRepository.
type pgArchiveRepository struct {
	pool *pgxpool.Pool
}

// NewArchiveRepository builds an ArchiveRepository backed by pool.
func NewArchiveRepository(pool *pgxpool.Pool) ArchiveRepository {
	return &pgArchiveRepository{pool: pool}
}

const folderExistsQuery = `
SELECT EXISTS (
    SELECT 1 FROM folders
    WHERE folder_id = $1::uuid
      AND deleted_at IS NULL
)`

// FolderExists implements ArchiveRepository.FolderExists.
func (r *pgArchiveRepository) FolderExists(ctx context.Context, folderID string) (bool, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, folderExistsQuery, folderID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check folder exists: %w", err)
	}
	return exists, nil
}

// folderTreeQuery walks the folder subtree rooted at $1. The root folder's
// own name is the first archive_path segment, so the archive extracts to a
// single top-level directory named after the root (e.g. "Docs/...").
const folderTreeQuery = `
WITH RECURSIVE folder_tree AS (
    SELECT
        f.folder_id,
        f.parent_folder_id,
        f.user_id,
        f.folder_name,
        f.folder_name::text AS archive_path
    FROM folders f
    WHERE f.folder_id = $1::uuid
      AND f.deleted_at IS NULL

    UNION ALL

    SELECT
        child.folder_id,
        child.parent_folder_id,
        child.user_id,
        child.folder_name,
        parent.archive_path || '/' || child.folder_name
    FROM folders child
    JOIN folder_tree parent
        ON child.parent_folder_id = parent.folder_id
    WHERE child.deleted_at IS NULL
)
SELECT folder_id, parent_folder_id, folder_name, archive_path
FROM folder_tree
ORDER BY archive_path`

// GetFolderTree implements ArchiveRepository.GetFolderTree.
func (r *pgArchiveRepository) GetFolderTree(ctx context.Context, rootFolderID string) ([]model.ArchiveFolder, error) {
	rows, err := r.pool.Query(ctx, folderTreeQuery, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("query folder tree: %w", err)
	}
	defer rows.Close()

	var folders []model.ArchiveFolder
	for rows.Next() {
		var f model.ArchiveFolder
		if err := rows.Scan(&f.FolderID, &f.ParentFolderID, &f.FolderName, &f.ArchivePath); err != nil {
			return nil, fmt.Errorf("scan folder row: %w", err)
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate folder rows: %w", err)
	}

	return folders, nil
}

// filesPageSize bounds how many rows GetFilesForFolderTree pulls per query.
// Large root folders can resolve to file counts that are risky to fetch in
// a single round trip (proxy/driver row caps, statement timeouts, memory);
// keyset pagination below fetches in bounded pages instead.
const filesPageSize = 5000

// filesForFolderTreePageQuery mirrors folderTreeQuery but joins in files and
// their latest version, using the idx_file_versions_latest index
// (file_id, file_version DESC) to make the per-file LATERAL lookup a
// single index scan rather than a sort. archive_path/file_id are resolved in
// the "resolved" CTE so the keyset predicate below can filter on them
// directly (a SELECT-list alias isn't visible in its own WHERE clause).
const filesForFolderTreePageQuery = `
WITH RECURSIVE folder_tree AS (
    SELECT
        f.folder_id,
        f.user_id,
        f.folder_name::text AS archive_path
    FROM folders f
    WHERE f.folder_id = $1::uuid
      AND f.deleted_at IS NULL

    UNION ALL

    SELECT
        child.folder_id,
        child.user_id,
        parent.archive_path || '/' || child.folder_name
    FROM folders child
    JOIN folder_tree parent
        ON child.parent_folder_id = parent.folder_id
    WHERE child.deleted_at IS NULL
),
resolved AS (
    SELECT
        f.file_id,
        f.file_name,
        ft.archive_path || '/' || f.file_name AS archive_path,
        fv.file_version,
        fv.hosted_at,
        fv.file_location,
        fv.file_size,
        fv.file_hash
    FROM folder_tree ft
    JOIN files f
        ON f.folder_id = ft.folder_id
       AND f.user_id = ft.user_id
       AND f.deleted_at IS NULL
    JOIN LATERAL (
        SELECT
            v.file_version,
            v.hosted_at,
            v.file_location,
            v.file_size,
            v.file_hash
        FROM file_versions v
        WHERE v.file_id = f.file_id
        ORDER BY v.file_version DESC
        LIMIT 1
    ) fv ON TRUE
)
SELECT file_id, file_name, archive_path, file_version, hosted_at, file_location, file_size, file_hash
FROM resolved
WHERE $2::uuid IS NULL OR (archive_path, file_id) > ($3::text, $2::uuid)
ORDER BY archive_path, file_id
LIMIT $4`

// GetFilesForFolderTree pages through the folder subtree's files using
// keyset pagination (cursor on archive_path, file_id) rather than a single
// unbounded query, so very large root folders can't blow past a proxy/driver
// row cap or hold an oversized result set in memory at once.
func (r *pgArchiveRepository) GetFilesForFolderTree(ctx context.Context, rootFolderID string) ([]model.ArchiveFile, error) {
	var files []model.ArchiveFile
	var cursorFileID, cursorPath *string

	for {
		page, err := r.getFilesPage(ctx, rootFolderID, cursorFileID, cursorPath)
		if err != nil {
			return nil, err
		}

		files = append(files, page...)

		if len(page) < filesPageSize {
			return files, nil
		}

		last := page[len(page)-1]
		cursorFileID = &last.FileID
		cursorPath = &last.ArchivePath
	}
}

// getFilesPage fetches one keyset page of at most filesPageSize files
// that sort after (cursorPath, cursorFileID). A nil cursor fetches the
// first page.
func (r *pgArchiveRepository) getFilesPage(ctx context.Context, rootFolderID string, cursorFileID, cursorPath *string) ([]model.ArchiveFile, error) {
	rows, err := r.pool.Query(ctx, filesForFolderTreePageQuery, rootFolderID, cursorFileID, cursorPath, filesPageSize)
	if err != nil {
		return nil, fmt.Errorf("query files for folder tree: %w", err)
	}
	defer rows.Close()

	var page []model.ArchiveFile
	for rows.Next() {
		var f model.ArchiveFile
		if err := rows.Scan(&f.FileID, &f.FileName, &f.ArchivePath, &f.FileVersion, &f.HostedAt, &f.FileLocation, &f.FileSize, &f.FileHash); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		page = append(page, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file rows: %w", err)
	}

	return page, nil
}
