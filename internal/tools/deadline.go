package tools

import (
	"context"
	"time"
)

type writeBudgetKey struct{}

// WithWriteBudget returns a context whose long confirm can extend the HTTP
// write deadline. fn receives the call budget. The server adds its own short
// write timeout. A nil fn leaves the deadline unchanged.
func WithWriteBudget(ctx context.Context, fn func(time.Duration) error) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, writeBudgetKey{}, fn)
}

// extendWriteBudget lengthens this response's write deadline for a confirm
// that is about to run an image action or an unknown budget. Other calls do
// not call it, so they stay on the server's short write timeout.
func extendWriteBudget(ctx context.Context, budget time.Duration) {
	fn, _ := ctx.Value(writeBudgetKey{}).(func(time.Duration) error)
	if fn == nil || budget <= 0 {
		return
	}
	_ = fn(budget)
}
