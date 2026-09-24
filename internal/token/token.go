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

// Package token issues and validates short-lived, job-scoped download
// tokens.
package token

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrInvalidToken is returned when a token does not match any issued
	// token, or does not match the job it is presented against.
	ErrInvalidToken = errors.New("invalid download token")
	// ErrTokenExpired is returned when a token matched but has passed its
	// expiry time.
	ErrTokenExpired = errors.New("download token expired")
)

const tokenBytes = 32

// Service issues and validates download tokens. Implementations must use a
// cryptographically secure random source. Kept behind an interface so a
// Redis-backed implementation can replace the in-memory one for
// multi-instance deployments without touching callers.
type Service interface {
	// Issue generates a new token scoped to jobID, valid for ttl.
	Issue(ctx context.Context, jobID string, ttl time.Duration) (string, error)
	// Validate checks that token is a currently-valid, unexpired token
	// issued for jobID.
	Validate(ctx context.Context, jobID string, token string) error
	// Revoke invalidates all tokens for jobID (used during job cleanup).
	Revoke(ctx context.Context, jobID string) error
}

// entry records which job a token was issued for and when it expires.
type entry struct {
	jobID     string
	expiresAt time.Time
}

// InMemoryService is the first-version Service implementation. Tokens live
// only in process memory: in a multi-instance deployment a download request
// must land on the same instance that issued the token (e.g. via sticky
// routing) unless/until this is replaced with a shared (Redis) store.
type InMemoryService struct {
	mu     sync.Mutex
	tokens map[string]entry
}

// NewInMemoryService creates an empty in-memory token service.
func NewInMemoryService() *InMemoryService {
	return &InMemoryService{tokens: make(map[string]entry)}
}

// Issue implements Service.Issue. Tokens are 32 bytes from crypto/rand,
// base64url-encoded without padding.
func (s *InMemoryService) Issue(_ context.Context, jobID string, ttl time.Duration) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	s.tokens[tok] = entry{jobID: jobID, expiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()

	return tok, nil
}

// Validate implements Service.Validate.
func (s *InMemoryService) Validate(_ context.Context, jobID string, tok string) error {
	s.mu.Lock()
	e, ok := s.tokens[tok]
	s.mu.Unlock()

	if !ok {
		return ErrInvalidToken
	}
	if subtle.ConstantTimeCompare([]byte(e.jobID), []byte(jobID)) != 1 {
		return ErrInvalidToken
	}
	if time.Now().After(e.expiresAt) {
		return ErrTokenExpired
	}
	return nil
}

// Revoke implements Service.Revoke.
func (s *InMemoryService) Revoke(_ context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, e := range s.tokens {
		if e.jobID == jobID {
			delete(s.tokens, tok)
		}
	}
	return nil
}
