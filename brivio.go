// Package brivio is the official Go SDK for the Brivio API — invoicing,
// e-Factura, contacts and more, for the Romanian market.
//
//	client, err := brivio.New(brivio.Config{APIKey: "brivio_sk_live_…"})
//	if err != nil { log.Fatal(err) }
//
//	page, err := client.Contacts().List(ctx, brivio.Query{"search": "srl"})
//
// This file is hand-written. Generated models and operation signatures live
// under ./generated and are regenerated wholesale by
// scripts/sdk/generate-sdks.mjs; the behaviour here (retries, error mapping,
// pagination, idempotency) is specified once in docs/SDK_CONTRACT.md so every
// official Brivio SDK behaves identically.
package brivio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public gateway.
	DefaultBaseURL = "https://api.brivio.ro/v1"
	// DefaultMaxRetries applies to transient failures only.
	DefaultMaxRetries = 2
	// DefaultTimeout is per request, not for the whole retry sequence.
	DefaultTimeout = 30 * time.Second
)

// retryableStatuses are transient. Any other 4xx is deterministic — retrying a
// 422 only delays the failure and burns the caller's context deadline.
var retryableStatuses = map[int]bool{429: true, 502: true, 503: true, 504: true}

// Error is returned by every SDK call that fails.
//
// Status is 0 when the request never completed (DNS failure, connection reset,
// timeout), so callers can distinguish "the API said no" from "we never
// reached the API" — the two need different handling.
type Error struct {
	Message string
	Code    string
	Status  int
	Details map[string][]string
}

func (e *Error) Error() string {
	return fmt.Sprintf("brivio: %s (code=%s status=%d)", e.Message, e.Code, e.Status)
}

// Config configures a Client. Only APIKey is required.
type Config struct {
	APIKey     string
	BaseURL    string
	MaxRetries int
	Timeout    time.Duration
	HTTPClient *http.Client
}

// Meta is the pagination block returned by list endpoints.
type Meta struct {
	Page       int `json:"page"`
	PerPage    int `json:"perPage"`
	Total      int `json:"total"`
	TotalPages int `json:"totalPages"`
}

// Page is one page of results plus its pagination metadata.
type Page struct {
	Data []json.RawMessage
	Meta *Meta
}

// Query holds query-string parameters. Nil values are omitted.
type Query map[string]any

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Details map[string][]string `json:"details"`
	} `json:"error"`
	Meta *Meta `json:"meta"`
}

// Client is the entry point. Safe for concurrent use.
type Client struct {
	apiKey     string
	baseURL    string
	maxRetries int
	httpClient *http.Client
}

// New validates the config and returns a Client.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("brivio: APIKey is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	maxRetries := cfg.MaxRetries
	if maxRetries == 0 && cfg.MaxRetries == 0 {
		maxRetries = DefaultMaxRetries
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = DefaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		maxRetries: maxRetries,
		httpClient: httpClient,
	}, nil
}

// RequestOption customises a single request.
type RequestOption func(*requestConfig)

type requestConfig struct {
	query          Query
	body           any
	idempotencyKey string
}

// WithQuery sets query-string parameters.
func WithQuery(q Query) RequestOption { return func(c *requestConfig) { c.query = q } }

// WithBody sets a JSON request body.
func WithBody(b any) RequestOption { return func(c *requestConfig) { c.body = b } }

// WithIdempotencyKey makes a mutating call safe to retry. The same key is
// reused across automatic retries, so a retried create cannot produce a second
// invoice.
func WithIdempotencyKey(key string) RequestOption {
	return func(c *requestConfig) { c.idempotencyKey = key }
}

// Do executes a request and returns the unwrapped `data` plus any `meta`.
//
// It is exported because the typed resources cannot cover every endpoint;
// reaching for net/http directly instead would lose retries, error mapping and
// envelope unwrapping.
func (c *Client) Do(ctx context.Context, method, path string, opts ...RequestOption) (json.RawMessage, *Meta, error) {
	cfg := &requestConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	endpoint := c.baseURL + path
	if len(cfg.query) > 0 {
		values := url.Values{}
		for k, v := range cfg.query {
			if v == nil {
				continue
			}
			values.Set(k, fmt.Sprint(v))
		}
		if encoded := values.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}
	}

	var payload []byte
	if cfg.body != nil {
		var err error
		if payload, err = json.Marshal(cfg.body); err != nil {
			return nil, nil, &Error{Message: err.Error(), Code: "INTERNAL_ERROR", Status: 0}
		}
	}

	var resp *http.Response
	var lastErr error

	for attempt := 0; ; attempt++ {
		// A fresh Reader per attempt: the previous one is drained.
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
		if err != nil {
			return nil, nil, &Error{Message: err.Error(), Code: "INTERNAL_ERROR", Status: 0}
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if cfg.idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", cfg.idempotencyKey)
		}

		resp, lastErr = c.httpClient.Do(req) //nolint:bodyclose // closed below
		retryable := lastErr != nil || retryableStatuses[resp.StatusCode]
		if !retryable || attempt >= c.maxRetries {
			break
		}

		delay := time.Duration(math.Min(math.Pow(2, float64(attempt))*500, 8000)) * time.Millisecond
		// Jitter is not decoration: without it every client that hit the same
		// outage retries in lockstep and recreates the spike.
		delay += time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
		if resp != nil {
			if after := resp.Header.Get("Retry-After"); after != "" {
				if secs, convErr := strconv.Atoi(after); convErr == nil {
					if wait := time.Duration(secs) * time.Second; wait > delay {
						delay = wait
					}
				}
			}
			resp.Body.Close()
		}

		select {
		case <-ctx.Done():
			return nil, nil, &Error{Message: ctx.Err().Error(), Code: "NETWORK_ERROR", Status: 0}
		case <-time.After(delay):
		}
	}

	if lastErr != nil {
		return nil, nil, &Error{Message: lastErr.Error(), Code: "NETWORK_ERROR", Status: 0}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, &Error{Message: err.Error(), Code: "NETWORK_ERROR", Status: 0}
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, &Error{
			Message: fmt.Sprintf("invalid JSON response (%d)", resp.StatusCode),
			Code:    "INTERNAL_ERROR",
			Status:  resp.StatusCode,
		}
	}
	if env.Error != nil {
		return nil, nil, &Error{
			Message: env.Error.Message,
			Code:    env.Error.Code,
			Status:  resp.StatusCode,
			Details: env.Error.Details,
		}
	}
	return env.Data, env.Meta, nil
}

// GetPage fetches one page of a list endpoint.
func (c *Client) GetPage(ctx context.Context, path string, query Query) (*Page, error) {
	data, meta, err := c.Do(ctx, http.MethodGet, path, WithQuery(query))
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if len(data) > 0 {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, &Error{Message: err.Error(), Code: "INTERNAL_ERROR", Status: 200}
		}
	}
	return &Page{Data: items, Meta: meta}, nil
}

// Iterate walks every page and calls fn for each item.
//
// A callback rather than a channel: a channel-based iterator leaks a goroutine
// whenever the caller stops early, and stopping early is the common case.
// Returning a non-nil error from fn stops iteration and surfaces that error.
func (c *Client) Iterate(ctx context.Context, path string, query Query, fn func(json.RawMessage) error) error {
	params := Query{}
	for k, v := range query {
		params[k] = v
	}
	page := 1
	if p, ok := params["page"].(int); ok {
		page = p
	}
	for {
		params["page"] = page
		result, err := c.GetPage(ctx, path, params)
		if err != nil {
			return err
		}
		for _, item := range result.Data {
			if err := fn(item); err != nil {
				return err
			}
		}
		// No meta means the endpoint is not paginated; stopping here avoids
		// re-fetching page 1 forever.
		if result.Meta == nil || len(result.Data) == 0 || page >= result.Meta.TotalPages {
			return nil
		}
		page++
	}
}
