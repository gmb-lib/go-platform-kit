package correlation_test

import (
	"strings"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-sig/go-platform-kit/correlation"
)

func TestValidID(t *testing.T) {
	valid := []string{
		"01JXY2T9ZD3F4G5H6J7K8M9N0P", // ULID
		"req-7",
		"a.b_c-D9",
		strings.Repeat("a", correlation.MaxIDLength),
	}
	for _, s := range valid {
		qt.Check(t, qt.IsTrue(correlation.ValidID(s)), qt.Commentf("%q must be accepted", s))
	}

	invalid := []string{
		"",
		strings.Repeat("a", correlation.MaxIDLength+1),
		"id with space",
		"id\r\nSet-Cookie: x=1", // header-injection attempt
		"id;rm -rf",
		"идентификатор", // non-ASCII
		"{\"json\":true}",
	}
	for _, s := range invalid {
		qt.Check(t, qt.IsFalse(correlation.ValidID(s)), qt.Commentf("%q must be rejected", s))
	}
}

// TestMiddleware_RejectsInvalidInboundID: an inbound correlation id that fails
// validation is ignored — the middleware substitutes a locally minted id rather
// than letting attacker-controlled text ride every log line and audit envelope.
func TestMiddleware_RejectsInvalidInboundID(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/cid", func(ctx *azugo.Context) {
		ctx.Text(correlation.ID(ctx))
	})
	app.Start(t)

	defer app.Stop()

	bad := "evil id with spaces; " + strings.Repeat("A", correlation.MaxIDLength)

	tc := app.TestClient()
	resp, err := tc.Get("/cid", tc.WithHeader(correlation.HeaderCorrelationID, bad))
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	got := string(body)
	qt.Check(t, qt.Not(qt.Equals(got, bad)))
	qt.Check(t, qt.Equals(len(got), 26)) // locally minted ULID
	qt.Check(t, qt.Equals(string(resp.Header.Peek(correlation.HeaderCorrelationID)), got))
}
