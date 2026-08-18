# Brivio SDK for Go

Official Go client for the [Brivio](https://brivio.ro) API — invoicing,
e-Factura, contacts, articles and more, for the Romanian market.

```bash
go get github.com/brivio-ro/brivio-sdk-go
```

## Quick start

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    brivio "github.com/brivio-ro/brivio-sdk-go"
)

func main() {
    client, err := brivio.New(brivio.Config{APIKey: "brivio_sk_live_…"})
    if err != nil {
        log.Fatal(err)
    }

    ctx := context.Background()

    // Verify the key and see which organization it belongs to.
    me, err := client.Me(ctx)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(string(me))

    // One page, with pagination metadata.
    page, err := client.Contacts().List(ctx, brivio.Query{"search": "srl"})
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%d of %d contacts\n", len(page.Data), page.Meta.Total)

    // Or walk every page — no manual page arithmetic.
    err = client.Contacts().Iterate(ctx, nil, func(raw json.RawMessage) error {
        var c struct{ Name string }
        if err := json.Unmarshal(raw, &c); err != nil {
            return err
        }
        fmt.Println(c.Name)
        return nil
    })
}
```

Iteration takes a callback rather than returning a channel: a channel-based
iterator leaks a goroutine whenever the caller stops early, and stopping early
is the common case. Returning an error from the callback stops iteration.

## Invoicing

```go
invoice, err := client.Invoices().Create(ctx,
    map[string]any{
        "contactId": "ct_123",
        "currency":  "RON",
        "lines": []map[string]any{
            {"description": "Consultanță", "quantity": 1, "unitPrice": 1000, "vatRate": 21},
        },
    },
    // Safe to retry: the same key never creates a second invoice.
    brivio.WithIdempotencyKey("order-42"),
)

client.Invoices().Send(ctx, invoiceID)
client.Invoices().SubmitEFactura(ctx, invoiceID)
```

## Errors

Every failure is a `*brivio.Error`:

```go
var apiErr *brivio.Error
if errors.As(err, &apiErr) {
    fmt.Println(apiErr.Code)    // "VALIDATION_ERROR"
    fmt.Println(apiErr.Status)  // 422
    fmt.Println(apiErr.Details) // map[name:[required]]
}
```

`Status == 0` means the request never reached the API (DNS, connection reset,
timeout) rather than the API rejecting it.

## Retries and timeouts

Transient failures (`429`, `502`, `503`, `504`, network errors) are retried
automatically — twice by default — with exponential backoff plus jitter, and
`Retry-After` is honoured on `429`. Other 4xx responses are **not** retried:
they fail identically every time.

```go
client, _ := brivio.New(brivio.Config{
    APIKey:     "…",
    MaxRetries: 5,                // 0 disables
    Timeout:    10 * time.Second, // per request
    BaseURL:    "https://api.brivio.ro/v1",
})
```

Cancelling the `context` aborts an in-flight request and any pending retry.

## Endpoints without a helper

The typed resources cover the common surface. Anything else stays one call
away and keeps retries, error mapping and envelope unwrapping:

```go
data, meta, err := client.Do(ctx, http.MethodGet, "/dns/zones")
```

## Typed models

Generated models for every schema live in the `generated` package:

```go
import "github.com/brivio-ro/brivio-sdk-go/generated"

var contact generated.Contact
_ = json.Unmarshal(raw, &contact)
```

## How this SDK is built

Models are generated from the published OpenAPI spec; the transport (retries,
errors, pagination, idempotency) is hand-written so every official Brivio SDK
behaves identically. See
[`SDK_CONTRACT.md`](https://github.com/dragoscv/brivio/blob/main/docs/SDK_CONTRACT.md).

## Links

- API reference — <https://brivio.ro/developers/docs>
- Other SDKs — [TypeScript](https://www.npmjs.com/package/@brivio/sdk), [Python](https://pypi.org/project/brivio/), [PHP](https://packagist.org/packages/brivio/sdk)

MIT © Brivio
