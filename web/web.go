// Package web holds small server-side helpers for working with an incoming
// request that every service shares, so request handling behaves identically
// across the fleet.
package web

import (
	"net/url"

	"azugo.io/azugo"
)

// PathParam returns a captured route parameter as its true, decoded value.
//
// The router matches and captures path segments from the raw request URI, so a
// captured parameter is still percent-encoded: a segment "a%20b" is handed back
// verbatim as "a%20b", not "a b". Reading it directly and then re-encoding it
// into an outbound URL double-encodes it ("a%2520b"); using it as a lookup key
// fails to match the real, decoded value. PathParam decodes it once so the caller
// gets the value the client actually sent. A segment that is not valid
// percent-encoding is returned unchanged (best effort — never worse than the raw
// value the caller would otherwise have used).
func PathParam(ctx *azugo.Context, key string) string {
	raw := ctx.Params.String(key)

	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}

	return decoded
}
