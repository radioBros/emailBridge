package accounts

import (
	"context"
	"sync"
)

// EraseHook runs during account erasure for resources that need more than a simple
// DeleteMany by accountId (e.g. S3 prefixes under a non-account key layout). Hooks are
// registered by other packages at Start via RegisterEraseHook to avoid import cycles.
type EraseHook func(ctx context.Context, organizationID, accountID string) error

var (
	eraseHooksMu sync.Mutex
	eraseHooks   []namedEraseHook
)

type namedEraseHook struct {
	name string
	fn   EraseHook
}

// RegisterEraseHook adds an erasure collaborator. Safe to call from module Start.
// Duplicate names replace the previous hook. fn must be idempotent.
func RegisterEraseHook(name string, fn EraseHook) {
	if name == "" || fn == nil {
		return
	}
	eraseHooksMu.Lock()
	defer eraseHooksMu.Unlock()
	for i, h := range eraseHooks {
		if h.name == name {
			eraseHooks[i].fn = fn
			return
		}
	}
	eraseHooks = append(eraseHooks, namedEraseHook{name: name, fn: fn})
}

func runEraseHooks(ctx context.Context, organizationID, accountID string) error {
	eraseHooksMu.Lock()
	hooks := append([]namedEraseHook(nil), eraseHooks...)
	eraseHooksMu.Unlock()
	for _, h := range hooks {
		if err := h.fn(ctx, organizationID, accountID); err != nil {
			return err
		}
	}
	return nil
}

// ClearEraseHooksForTest removes every hook (tests only).
func ClearEraseHooksForTest() {
	eraseHooksMu.Lock()
	eraseHooks = nil
	eraseHooksMu.Unlock()
}

// RunEraseHooksForTest invokes registered hooks (tests only).
func RunEraseHooksForTest(ctx context.Context, organizationID, accountID string) error {
	return runEraseHooks(ctx, organizationID, accountID)
}
