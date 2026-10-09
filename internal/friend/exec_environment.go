package friend

import "context"

// WithExecEnvironment passes an explicit command environment to RealExec
// privately through os/exec, rather than placing its values in arguments.
func WithExecEnvironment(ctx context.Context, env []string) context.Context {
	return context.WithValue(ctx, execEnvironmentKey{}, append([]string{}, env...))
}

// WithExecOutputLimit bounds both stored streams of RealExec and cancels its
// verified group when either exceeds the explicitly configured byte limit.
func WithExecOutputLimit(ctx context.Context, limit int) context.Context {
	return context.WithValue(ctx, execOutputLimitKey{}, limit)
}
