package observability

import (
	"context"
	"log/slog"
	"runtime/debug"
)

// Go runs fn in a goroutine with recover(). On panic it logs to the crash category
// (caller passes logging.System.For(logging.CatCrash, name)), reports via Reporter, and does not re-panic.
func Go(ctx context.Context, name string, log *slog.Logger, r *Reporter, fn func(ctx context.Context)) {
	go func() {
		defer Recover(ctx, name, log, r)
		fn(ctx)
	}()
}

// Recover is meant to be deferred in an existing goroutine.
func Recover(ctx context.Context, name string, log *slog.Logger, r *Reporter) {
	rec := recover()
	if rec == nil {
		return
	}
	stack := debug.Stack()
	if log != nil {
		log.ErrorContext(ctx, "goroutine panic recovered",
			"goroutine", name,
			"panic", rec,
			"stack", string(stack),
		)
	}
	if r != nil {
		r.CapturePanic(ctx, rec, stack, map[string]string{"component": name})
	}
}
