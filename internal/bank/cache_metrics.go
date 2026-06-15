package bank

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// instrumentedCache wraps a Cache with OpenTelemetry metrics: a request counter
// (labeled by backend, operation, and hit/miss result) and an operation-latency
// histogram. It is a near-zero-cost no-op when no meter provider is installed.
type instrumentedCache struct {
	inner   Cache
	backend string
	reqs    metric.Int64Counter
	latency metric.Float64Histogram
}

func newInstrumentedCache(inner Cache, backend string) Cache {
	m := otel.Meter("github.com/ngoldack/fin-mcp/internal/bank")
	reqs, _ := m.Int64Counter(
		"fin_mcp.cache.requests",
		metric.WithDescription("Cache requests by backend, operation and result"),
	)
	latency, _ := m.Float64Histogram(
		"fin_mcp.cache.operation.duration",
		metric.WithDescription("Cache operation latency"),
		metric.WithUnit("s"),
	)
	return &instrumentedCache{inner: inner, backend: backend, reqs: reqs, latency: latency}
}

func (c *instrumentedCache) record(ctx context.Context, op, result string, start time.Time) {
	attrs := metric.WithAttributes(
		attribute.String("cache.backend", c.backend),
		attribute.String("operation", op),
		attribute.String("result", result),
	)
	c.reqs.Add(ctx, 1, attrs)
	c.latency.Record(ctx, time.Since(start).Seconds(), attrs)
}

func hitMiss(ok bool) string {
	if ok {
		return "hit"
	}
	return "miss"
}

func (c *instrumentedCache) GetAccounts(ctx context.Context) ([]Account, bool) {
	start := time.Now()
	v, ok := c.inner.GetAccounts(ctx)
	c.record(ctx, "get_accounts", hitMiss(ok), start)
	return v, ok
}

func (c *instrumentedCache) SetAccounts(ctx context.Context, accounts []Account) {
	start := time.Now()
	c.inner.SetAccounts(ctx, accounts)
	c.record(ctx, "set_accounts", "set", start)
}

func (c *instrumentedCache) GetDetail(ctx context.Context, accountID string) (AccountDetail, bool) {
	start := time.Now()
	v, ok := c.inner.GetDetail(ctx, accountID)
	c.record(ctx, "get_detail", hitMiss(ok), start)
	return v, ok
}

func (c *instrumentedCache) SetDetail(ctx context.Context, accountID string, detail AccountDetail) {
	start := time.Now()
	c.inner.SetDetail(ctx, accountID, detail)
	c.record(ctx, "set_detail", "set", start)
}

func (c *instrumentedCache) Clear(ctx context.Context) {
	start := time.Now()
	c.inner.Clear(ctx)
	c.record(ctx, "clear", "ok", start)
}

func (c *instrumentedCache) Close() error { return c.inner.Close() }
