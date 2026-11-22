package vendor

import "context"

// HasECHRecord is the exported, package-level wrapper used by the checker's
// ECH injector (internal/client) to ask whether a node's server domain can
// actually do ECH (HTTPS type65 record with ech= value). See doh.go for the
// rationale: mihomo's ech-opts{enable:true} hard-fails the dial when the
// record is missing, so blind injection marks healthy non-CF nodes dead.
func HasECHRecord(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	_, err := ResolveECH(ctx, host)
	return err == nil
}

// ResolveECH returns the HTTPS record's decoded ECHConfigList.
func ResolveECH(ctx context.Context, host string) ([]byte, error) {
	return globalDoH.ResolveECH(ctx, host)
}
