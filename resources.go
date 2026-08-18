package brivio

import (
	"context"
	"encoding/json"
	"net/http"
)

// Resource is CRUD over one collection.
//
// A uniform wrapper rather than 139 generated methods: the generated layer
// exists for models and exact operation signatures, but callers reach for
// client.Contacts().List() far more often than for a specific operation id.
type Resource struct {
	client *Client
	path   string
}

// List returns one page. Use Iterate to walk them all.
func (r *Resource) List(ctx context.Context, query Query) (*Page, error) {
	return r.client.GetPage(ctx, r.path, query)
}

// Iterate walks every page, calling fn per item.
func (r *Resource) Iterate(ctx context.Context, query Query, fn func(json.RawMessage) error) error {
	return r.client.Iterate(ctx, r.path, query, fn)
}

// Get fetches one record.
func (r *Resource) Get(ctx context.Context, id string) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodGet, r.path+"/"+id)
	return data, err
}

// Create inserts a record. Pass WithIdempotencyKey to make it retry-safe.
func (r *Resource) Create(ctx context.Context, body any, opts ...RequestOption) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodPost, r.path, append([]RequestOption{WithBody(body)}, opts...)...)
	return data, err
}

// Update patches a record.
func (r *Resource) Update(ctx context.Context, id string, body any) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodPatch, r.path+"/"+id, WithBody(body))
	return data, err
}

// Delete removes a record.
func (r *Resource) Delete(ctx context.Context, id string) error {
	_, _, err := r.client.Do(ctx, http.MethodDelete, r.path+"/"+id)
	return err
}

// Invoices adds the invoice lifecycle actions to the standard CRUD surface.
type Invoices struct{ Resource }

// Send marks an invoice as sent (DRAFT → SENT). Idempotent server-side.
func (r *Invoices) Send(ctx context.Context, id string) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodPost, r.path+"/"+id+"/send")
	return data, err
}

// SubmitEFactura submits an invoice to ANAF e-Factura.
func (r *Invoices) SubmitEFactura(ctx context.Context, id string) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodPost, r.path+"/"+id+"/submit-efactura")
	return data, err
}

// EFacturaStatus reports the ANAF transmission state.
func (r *Invoices) EFacturaStatus(ctx context.Context, id string) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodGet, r.path+"/"+id+"/efactura")
	return data, err
}

// Payments lists payments recorded against an invoice.
func (r *Invoices) Payments(ctx context.Context, id string) (*Page, error) {
	return r.client.GetPage(ctx, r.path+"/"+id+"/payments", nil)
}

// AddPayment records a payment and updates the invoice status.
func (r *Invoices) AddPayment(ctx context.Context, id string, body any, opts ...RequestOption) (json.RawMessage, error) {
	data, _, err := r.client.Do(ctx, http.MethodPost, r.path+"/"+id+"/payments",
		append([]RequestOption{WithBody(body)}, opts...)...)
	return data, err
}

// Contacts returns the contacts resource.
func (c *Client) Contacts() *Resource { return &Resource{c, "/contacts"} }

// Articles returns the articles resource.
func (c *Client) Articles() *Resource { return &Resource{c, "/articles"} }

// Invoices returns the invoices resource, including lifecycle actions.
func (c *Client) Invoices() *Invoices { return &Invoices{Resource{c, "/invoices"}} }

// Locations returns the locations resource.
func (c *Client) Locations() *Resource { return &Resource{c, "/locations"} }

// Projects returns the projects resource.
func (c *Client) Projects() *Resource { return &Resource{c, "/projects"} }

// Quotes returns the quotes resource.
func (c *Client) Quotes() *Resource { return &Resource{c, "/quotes"} }

// Expenses returns the expenses resource.
func (c *Client) Expenses() *Resource { return &Resource{c, "/expenses"} }

// Contracts returns the contracts resource.
func (c *Client) Contracts() *Resource { return &Resource{c, "/contracts"} }

// Documents returns the documents resource.
func (c *Client) Documents() *Resource { return &Resource{c, "/documents"} }

// TimeEntries returns the time-entries resource.
func (c *Client) TimeEntries() *Resource { return &Resource{c, "/time-entries"} }

// Me identifies the authenticated organization — the cheapest way to verify a
// key is valid.
func (c *Client) Me(ctx context.Context) (json.RawMessage, error) {
	data, _, err := c.Do(ctx, http.MethodGet, "/me")
	return data, err
}
