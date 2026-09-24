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

package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	tempSuffix  = ".zip.tmp"
	finalSuffix = ".zip"
)

// LocalArchiveStorage stores archive files on the local filesystem under a
// single directory. In a horizontally scaled deployment this directory is
// instance-local: a download request landing on a different instance than
// the one that generated the archive will not find it. See the README's
// "Scaling" section for the shared/object-storage upgrade path.
//
// Archives are always stored as "<job-id>.zip" regardless of export type;
// the download handler derives Content-Type from the job, not the filename.
type LocalArchiveStorage struct {
	dir string
}

// NewLocalArchiveStorage creates the archive directory (if missing) and
// returns a LocalArchiveStorage rooted at it.
func NewLocalArchiveStorage(dir string) (*LocalArchiveStorage, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create archive temp dir: %w", err)
	}
	return &LocalArchiveStorage{dir: dir}, nil
}

// tempPath returns the in-progress archive path for jobID.
func (s *LocalArchiveStorage) tempPath(jobID string) string {
	return filepath.Join(s.dir, jobID+tempSuffix)
}

// finalPath returns the finalized archive path for jobID.
func (s *LocalArchiveStorage) finalPath(jobID string) string {
	return filepath.Join(s.dir, jobID+finalSuffix)
}

// Create implements ArchiveStorage.Create.
func (s *LocalArchiveStorage) Create(jobID string) (io.WriteCloser, error) {
	f, err := os.OpenFile(s.tempPath(jobID), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return nil, fmt.Errorf("create temp archive: %w", err)
	}
	return f, nil
}

// Open implements ArchiveStorage.Open.
func (s *LocalArchiveStorage) Open(jobID string) (io.ReadCloser, error) {
	f, err := os.Open(s.finalPath(jobID))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	return f, nil
}

// Delete implements ArchiveStorage.Delete.
func (s *LocalArchiveStorage) Delete(jobID string) error {
	for _, p := range []string{s.tempPath(jobID), s.finalPath(jobID)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete %s: %w", p, err)
		}
	}
	return nil
}

// RenameTemp implements ArchiveStorage.RenameTemp using os.Rename, which
// is atomic within a single filesystem.
func (s *LocalArchiveStorage) RenameTemp(jobID string) error {
	if err := os.Rename(s.tempPath(jobID), s.finalPath(jobID)); err != nil {
		return fmt.Errorf("finalize archive: %w", err)
	}
	return nil
}

// Size implements ArchiveStorage.Size.
func (s *LocalArchiveStorage) Size(jobID string) (int64, error) {
	info, err := os.Stat(s.finalPath(jobID))
	if err != nil {
		return 0, fmt.Errorf("stat archive: %w", err)
	}
	return info.Size(), nil
}

// ListJobIDs implements ArchiveStorage.ListJobIDs.
func (s *LocalArchiveStorage) ListJobIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read archive dir: %w", err)
	}

	seen := make(map[string]struct{})
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var id string
		switch {
		case strings.HasSuffix(name, tempSuffix):
			id = strings.TrimSuffix(name, tempSuffix)
		case strings.HasSuffix(name, finalSuffix):
			id = strings.TrimSuffix(name, finalSuffix)
		default:
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}
