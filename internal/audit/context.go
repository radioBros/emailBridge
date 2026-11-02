package audit

import "context"

type userAgentKey struct{}

// WithUserAgent stores the request User-Agent for audit records.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	return context.WithValue(ctx, userAgentKey{}, ua)
}

// UserAgentFrom returns the User-Agent stored by WithUserAgent.
func UserAgentFrom(ctx context.Context) string {
	s, _ := ctx.Value(userAgentKey{}).(string)
	return s
}
