package brivio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These mirror the Python SDK's transport tests on purpose: docs/SDK_CONTRACT.md
// requires identical behaviour from every official SDK, so the same cases must
// pass in each language. A divergence here is a contract bug, not a Go bug.

// newTestClient points a Client at a test server with retry delays kept short.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: "k", BaseURL: server.URL, MaxRetries: 2, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client, server
}

func TestUnwrapsEnvelope(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"c1"},"error":null}`))
	})
	data, _, err := client.Do(context.Background(), http.MethodGet, "/contacts/c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"id":"c1"}` {
		t.Fatalf("got %s", data)
	}
}

func TestRaisesAPIErrorWithCodeAndDetails(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"VALIDATION_ERROR","message":"name is required","details":{"name":["required"]}}}`))
	})
	_, _, err := client.Do(context.Background(), http.MethodPost, "/contacts", WithBody(map[string]any{}))
	var brivioErr *Error
	if !asError(err, &brivioErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if brivioErr.Code != "VALIDATION_ERROR" || brivioErr.Status != 422 {
		t.Fatalf("got code=%s status=%d", brivioErr.Code, brivioErr.Status)
	}
	if got := brivioErr.Details["name"]; len(got) != 1 || got[0] != "required" {
		t.Fatalf("details not surfaced: %v", brivioErr.Details)
	}
}

func TestNetworkFailureIsStatusZero(t *testing.T) {
	// Callers must be able to tell "never reached the API" from a 4xx/5xx.
	client, err := New(Config{APIKey: "k", BaseURL: "http://127.0.0.1:1", MaxRetries: 0, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Do(context.Background(), http.MethodGet, "/me")
	var brivioErr *Error
	if !asError(err, &brivioErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if brivioErr.Status != 0 || brivioErr.Code != "NETWORK_ERROR" {
		t.Fatalf("got code=%s status=%d", brivioErr.Code, brivioErr.Status)
	}
}

func TestNonJSONSuccessBodyIsReported(t *testing.T) {
	// A proxy returning an HTML page with 200 must not look like an empty
	// successful response.
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>hello</html>`))
	})
	_, _, err := client.Do(context.Background(), http.MethodGet, "/me")
	var brivioErr *Error
	if !asError(err, &brivioErr) || brivioErr.Code != "INTERNAL_ERROR" {
		t.Fatalf("expected INTERNAL_ERROR, got %v", err)
	}
}

func TestRetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"data":null,"error":{"code":"X","message":"down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"ok":true},"error":null}`))
	})
	if _, _, err := client.Do(context.Background(), http.MethodGet, "/me"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestDoesNotRetryDeterministicClientErrors(t *testing.T) {
	// A 422 fails identically every time; retrying only burns the deadline.
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"VALIDATION_ERROR","message":"no"}}`))
	})
	if _, _, err := client.Do(context.Background(), http.MethodPost, "/contacts", WithBody(map[string]any{})); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"X","message":"down"}}`))
	})
	if _, _, err := client.Do(context.Background(), http.MethodGet, "/me"); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 3 { // initial + 2 retries
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestIdempotencyKeyStableAcrossRetries(t *testing.T) {
	// A fresh key per attempt would let a retried create make two invoices.
	var keys []string
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"data":null,"error":{"code":"X","message":"down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"i1"},"error":null}`))
	})
	if _, _, err := client.Do(context.Background(), http.MethodPost, "/invoices",
		WithBody(map[string]any{}), WithIdempotencyKey("order-42")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range keys {
		if k != "order-42" {
			t.Fatalf("idempotency key changed across retries: %v", keys)
		}
	}
}

func TestRetriedRequestResendsBody(t *testing.T) {
	// The body Reader is drained by the first attempt; a retry must rebuild it
	// or the server sees an empty body on the attempt that actually succeeds.
	var bodies []string
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"data":null,"error":{"code":"X","message":"down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"ok":true},"error":null}`))
	})
	if _, _, err := client.Do(context.Background(), http.MethodPost, "/contacts",
		WithBody(map[string]any{"name": "Acme"})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[1] == "" {
		t.Fatalf("body not resent identically: %#v", bodies)
	}
}

func TestIteratesEveryPage(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"data":[{"id":2}],"error":null,"meta":{"page":2,"perPage":1,"total":2,"totalPages":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":1}],"error":null,"meta":{"page":1,"perPage":1,"total":2,"totalPages":2}}`))
	})
	var ids []int
	err := client.Iterate(context.Background(), "/contacts", nil, func(raw json.RawMessage) error {
		var item struct{ ID int }
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		ids = append(ids, item.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("got %v", ids)
	}
}

func TestIterateTerminatesWithoutMeta(t *testing.T) {
	// No metadata must end iteration, not loop forever re-fetching page 1.
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data":[{"id":1}],"error":null}`))
	})
	count := 0
	if err := client.Iterate(context.Background(), "/contacts", nil, func(json.RawMessage) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 || calls != 1 {
		t.Fatalf("count=%d calls=%d", count, calls)
	}
}

func TestEmptyAPIKeyFailsImmediately(t *testing.T) {
	if _, err := New(Config{APIKey: ""}); err == nil {
		t.Fatal("expected an error for an empty API key")
	}
}

func TestQuerySkipsNilValues(t *testing.T) {
	var gotURL string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = w.Write([]byte(`{"data":[],"error":null}`))
	})
	if _, err := client.GetPage(context.Background(), "/contacts", Query{"search": "srl", "page": nil}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotURL, "search=srl") || strings.Contains(gotURL, "page=") {
		t.Fatalf("unexpected query: %s", gotURL)
	}
}

// asError is errors.As, spelled out to keep the import list minimal.
func asError(err error, target **Error) bool {
	if err == nil {
		return false
	}
	casted, ok := err.(*Error)
	if ok {
		*target = casted
	}
	return ok
}
