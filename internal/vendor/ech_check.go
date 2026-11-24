package vendor

import "context"

// ResolveECH returns the HTTPS record's decoded ECHConfigList.
//
// The checker's ECH assessment (internal/checker/initial) resolves through this
// wrapper. mihomo reaches the same resolver through resolver.DefaultResolver.
//
// HasECHRecord lived here until 2026-11-25 as the gate for the client-package
// ECH injector. That injector was removed with its only caller, so the gate went
// with it; the lookup it performed is exactly what ResolveECH already does.
func ResolveECH(ctx context.Context, host string) ([]byte, error) {
	return globalDoH.ResolveECH(ctx, host)
}
