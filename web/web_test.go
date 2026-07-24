package web_test

import (
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/web"
)

// TestPathParam_DecodesCapturedSegment drives a real request through the router
// so the captured parameter is whatever the router actually hands a handler. The
// router captures the raw percent-encoded segment, so reading it directly yields
// "a%20b"; PathParam must return the decoded "a b". This is the behaviour that,
// when skipped, double-encodes a filename on an outbound call.
func TestPathParam_DecodesCapturedSegment(t *testing.T) {
	cases := []struct {
		name    string
		segment string // what the client puts in the path
		rawWant string // what the router captures verbatim
		want    string // what PathParam returns
	}{
		{"space", "a%20b.png", "a%20b.png", "a b.png"},
		{"already-decoded has nothing to undo", "plain.png", "plain.png", "plain.png"},
		{"plus stays literal in a path", "a+b.png", "a+b.png", "a+b.png"},
		{"unicode", "%C3%A4.png", "%C3%A4.png", "ä.png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := azugo.NewTestApp()

			var rawSeen, decodedSeen string
			app.Get("/f/{name}", func(ctx *azugo.Context) {
				rawSeen = ctx.Params.String("name")
				decodedSeen = web.PathParam(ctx, "name")
				ctx.StatusCode(fasthttp.StatusNoContent)
			})
			app.Start(t)
			defer app.Stop()

			resp, err := app.TestClient().Get("/f/" + tc.segment)
			qt.Assert(t, qt.IsNil(err))
			defer fasthttp.ReleaseResponse(resp)

			qt.Check(t, qt.Equals(rawSeen, tc.rawWant))
			qt.Check(t, qt.Equals(decodedSeen, tc.want))
		})
	}
}
