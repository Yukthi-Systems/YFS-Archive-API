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

// Package client contains outbound HTTP communication with the storage
// servers that hold actual file bytes.
package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StorageClient fetches file content from a named storage server. Callers
// must close the returned response body. Implementations must reuse
// connections (via a shared, pooled http.Client) rather than dialing a new
// connection per request, and must never buffer the full response body
// themselves.
type StorageClient interface {
	// Get fetches location's content from the server identified by
	// hostAddress (a servers.host_address value, e.g.
	// "https://store-1.files.example").
	Get(ctx context.Context, hostAddress string, location string) (*http.Response, error)
}

// ServerConfig is a single storage server's connection details: its base URL
// (the servers.host_address value) and the API key (servers.secret_key)
// this service presents to authenticate requests for file data. Both are
// resolved from the database (see repository.StorageServerRepository), not
// the process environment.
type ServerConfig struct {
	BaseURL string
	APIKey  string
}

// storageAPITokenHeader is the header this service sends a storage server's
// secret key on, when requesting file data.
const storageAPITokenHeader = "X-API-Token"

// downloadPath is the fixed route on every storage server that streams a
// file's content back, given a file_location query parameter (the raw
// file_versions.file_location value). GET + query param rather than
// embedding the location in the URL path avoids ambiguity/escaping issues
// with the long, slash-heavy paths this service stores (S3's GetObject uses
// the same GET-for-retrieval shape, keyed instead by path since its object
// keys don't contain awkward characters).
const downloadPath = "/internal/files/download"

// httpStorageClient is the production StorageClient backed by a shared,
// connection-pooled http.Client.
type httpStorageClient struct {
	client  *http.Client
	servers map[string]ServerConfig // host_address -> connection details
}

// NewHTTPStorageClient builds a StorageClient for the given
// host_address->config map. requestTimeout bounds only the time to receive
// response headers
// (connect + TLS + server processing), not the time spent streaming a large
// response body, so large-file downloads are not cut off mid-stream.
func NewHTTPStorageClient(servers map[string]ServerConfig, requestTimeout time.Duration) StorageClient {
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: requestTimeout,
	}

	serverCopy := make(map[string]ServerConfig, len(servers))
	for k, v := range servers {
		v.BaseURL = strings.TrimSuffix(v.BaseURL, "/")
		serverCopy[k] = v
	}

	return &httpStorageClient{
		client:  &http.Client{Transport: transport},
		servers: serverCopy,
	}
}

// ErrUnknownServer indicates a file_versions.hosted_at value with no
// matching, configured servers row.
type ErrUnknownServer struct {
	HostAddress string
}

// Error implements the error interface.
func (e *ErrUnknownServer) Error() string {
	return fmt.Sprintf("unknown storage server %q", e.HostAddress)
}

// ErrStorageResponse indicates the storage server responded with a
// non-2xx status.
type ErrStorageResponse struct {
	HostAddress string
	StatusCode  int
}

// Error implements the error interface.
func (e *ErrStorageResponse) Error() string {
	return fmt.Sprintf("storage server %q returned status %d", e.HostAddress, e.StatusCode)
}

// Get implements StorageClient.Get. It sends
// GET {base_url}/internal/files/download?file_location=... with the
// server's key in the X-API-Token header. Non-2xx responses are drained,
// closed and returned as *ErrStorageResponse.
func (c *httpStorageClient) Get(ctx context.Context, hostAddress string, location string) (*http.Response, error) {
	cfg, ok := c.servers[hostAddress]
	if !ok {
		return nil, &ErrUnknownServer{HostAddress: hostAddress}
	}

	reqURL, err := url.Parse(cfg.BaseURL + downloadPath)
	if err != nil {
		return nil, fmt.Errorf("build storage url: %w", err)
	}
	q := reqURL.Query()
	q.Set("file_location", location)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build storage request: %w", err)
	}
	if cfg.APIKey != "" {
		req.Header.Set(storageAPITokenHeader, cfg.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request file from storage server %q: %w", hostAddress, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
		}()
		return nil, &ErrStorageResponse{HostAddress: hostAddress, StatusCode: resp.StatusCode}
	}

	return resp, nil
}
