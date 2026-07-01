package errors

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
)

func TestNewProblem_DerivesStatusAndTitleFromCode(t *testing.T) {
	p := NewProblem("err:document:notFound")

	qt.Check(t, qt.Equals(p.Code, "err:document:notFound"))
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusNotFound))
	qt.Check(t, qt.Equals(p.Title, "Not found"))
}

func TestNewProblem_UnknownCodeIsSafe500(t *testing.T) {
	p := NewProblem("not-a-code")

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusInternalServerError))
	qt.Check(t, qt.Equals(p.Title, "Internal server error"))
}

func TestNewProblem_TitleFollowsStatusForUnknownReason(t *testing.T) {
	// A domain-specific reason outside the taxonomy (legalHold) has no derived
	// title, so it follows the overridden status rather than defaulting to the
	// 500 title — the emitter keeps the precise code without a misleading title.
	p := NewProblem("err:document:legalHold", WithStatus(fasthttp.StatusConflict))

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusConflict))
	qt.Check(t, qt.Equals(p.Title, "Conflict"))
}

func TestNewProblem_Options(t *testing.T) {
	p := NewProblem("err:document:notFound",
		WithStatus(fasthttp.StatusGone),
		WithTitle("Gone for good"),
		WithDetail("document 42 purged on TTL"),
		WithSource("document-store"),
	)

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusGone))
	qt.Check(t, qt.Equals(p.Title, "Gone for good"))
	qt.Check(t, qt.Equals(p.Detail, "document 42 purged on TTL"))
	qt.Check(t, qt.Equals(p.Source, "document-store"))
	qt.Check(t, qt.IsFalse(p.detailPublic))
}

func TestProblem_StatusAndSafeError(t *testing.T) {
	p := NewProblem("err:document:notFound", WithDetail("internal detail"))

	qt.Check(t, qt.Equals(p.StatusCode(), fasthttp.StatusNotFound))
	// SafeError is the stable title, never the occurrence detail.
	qt.Check(t, qt.Equals(p.SafeError(), "Not found"))
	// Error (log side) carries the code and the most specific message.
	qt.Check(t, qt.Equals(p.Error(), "err:document:notFound: internal detail"))
}

// TestPublic_StripsSourceAndChainStructurally proves the public projection can
// never carry the internal topology fields — they are absent from the type, so
// the marshalled body has no source/chain keys regardless of what was set.
func TestPublic_StripsSourceAndChainStructurally(t *testing.T) {
	p := NewProblem("err:document:notFound",
		WithSource("document-store"),
		WithDetail("internal only detail"),
	)
	p.TraceID = "01TRACE"
	p.Chain = []Hop{{Service: "document-store", Code: p.Code, Status: p.Status}}

	body, err := json.Marshal(p.Public())
	qt.Assert(t, qt.IsNil(err))

	s := string(body)
	qt.Check(t, qt.IsFalse(strings.Contains(s, "source")), qt.Commentf("public body leaked source: %s", s))
	qt.Check(t, qt.IsFalse(strings.Contains(s, "chain")), qt.Commentf("public body leaked chain: %s", s))
	// Detail is internal-by-default: dropped unless marked public-safe.
	qt.Check(t, qt.IsFalse(strings.Contains(s, "internal only detail")), qt.Commentf("public body leaked detail: %s", s))
	// The safe fields survive.
	qt.Check(t, qt.IsTrue(strings.Contains(s, "err:document:notFound")))
	qt.Check(t, qt.IsTrue(strings.Contains(s, "01TRACE")))
	qt.Check(t, qt.IsTrue(strings.Contains(s, "Not found")))
}

func TestPublic_KeepsPublicSafeDetail(t *testing.T) {
	p := NewProblem("err:document:invalid", WithPublicDetail("the file must be a PDF"))

	qt.Check(t, qt.IsTrue(p.detailPublic))
	qt.Check(t, qt.Equals(p.Public().Detail, "the file must be a PDF"))
}

func TestMarshalError_ProblemJSON(t *testing.T) {
	p := NewProblem("err:document:notFound", WithSource("document-store"))

	body, ct, ok := p.MarshalError("application/json")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(ct, ContentTypeProblemJSON))

	var got Problem
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &got)))
	qt.Check(t, qt.Equals(got.Code, "err:document:notFound"))
	qt.Check(t, qt.Equals(got.Status, fasthttp.StatusNotFound))
}

func TestMarshalError_FallsBackForNonJSON(t *testing.T) {
	p := NewProblem("err:document:notFound")

	_, _, ok := p.MarshalError("application/xml")
	qt.Check(t, qt.IsFalse(ok))
}

func TestParseProblem_Roundtrip(t *testing.T) {
	orig := NewProblem("err:document:notFound", WithSource("document-store"))
	orig.TraceID = "01TRACE"
	body, err := json.Marshal(orig)
	qt.Assert(t, qt.IsNil(err))

	got, ok := ParseProblem(body)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(got.Code, "err:document:notFound"))
	qt.Check(t, qt.Equals(got.Source, "document-store"))
	qt.Check(t, qt.Equals(got.TraceID, "01TRACE"))
	qt.Check(t, qt.Equals(got.Status, fasthttp.StatusNotFound))
}

func TestParseProblem_RejectsNonProblem(t *testing.T) {
	_, ok := ParseProblem([]byte(`{"foo":"bar"}`))
	qt.Check(t, qt.IsFalse(ok))

	_, ok = ParseProblem(nil)
	qt.Check(t, qt.IsFalse(ok))

	_, ok = ParseProblem([]byte(`not json`))
	qt.Check(t, qt.IsFalse(ok))
}

func TestRelay_PreservesTerminalAndAppendsHop(t *testing.T) {
	down := NewProblem("err:document:notFound", WithSource("document-store"))
	down.TraceID = "01TRACE"
	down.Chain = []Hop{{Service: "document-store", Code: down.Code, Status: fasthttp.StatusNotFound}}

	relayed := Relay(down, "portal-api", fasthttp.StatusNotFound)

	// Terminal attribution is preserved.
	qt.Check(t, qt.Equals(relayed.Code, "err:document:notFound"))
	qt.Check(t, qt.Equals(relayed.Source, "document-store"))
	qt.Check(t, qt.Equals(relayed.TraceID, "01TRACE"))
	qt.Check(t, qt.Equals(relayed.Status, fasthttp.StatusNotFound))

	// This service is appended; the root (origin) stays first.
	qt.Assert(t, qt.Equals(len(relayed.Chain), 2))
	qt.Check(t, qt.Equals(relayed.Chain[0].Service, "document-store"))
	qt.Check(t, qt.Equals(relayed.Chain[1].Service, "portal-api"))

	// Relay does not mutate the downstream problem's chain.
	qt.Check(t, qt.Equals(len(down.Chain), 1))
}

func TestRelay_SeedsOriginWhenNoChain(t *testing.T) {
	down := NewProblem("err:envelope:depFailed", WithSource("envelope"), WithStatus(fasthttp.StatusFailedDependency))

	relayed := Relay(down, "portal-api", fasthttp.StatusFailedDependency)

	qt.Assert(t, qt.Equals(len(relayed.Chain), 2))
	qt.Check(t, qt.Equals(relayed.Chain[0].Service, "envelope"))
	qt.Check(t, qt.Equals(relayed.Chain[0].Status, fasthttp.StatusFailedDependency))
	qt.Check(t, qt.Equals(relayed.Chain[1].Service, "portal-api"))
}

func TestRelay_NilDownstreamIsUpstreamUnavailable(t *testing.T) {
	relayed := Relay(nil, "portal-api", fasthttp.StatusBadGateway)

	qt.Check(t, qt.Equals(relayed.Code, "err:upstream:unavailable"))
	qt.Check(t, qt.Equals(relayed.Status, fasthttp.StatusBadGateway))
	qt.Check(t, qt.Equals(relayed.Source, "portal-api"))
}

func TestBoundChain_KeepsRootAndElidesMiddle(t *testing.T) {
	chain := make([]Hop, 0, 12)
	for i := 0; i < 12; i++ {
		chain = append(chain, Hop{Service: string(rune('a' + i)), Status: 500})
	}
	chain[0].Service = "root"
	chain[11].Service = "latest"

	got := boundChain(chain)

	qt.Assert(t, qt.Equals(len(got), MaxChainHops))
	// Root is always kept.
	qt.Check(t, qt.Equals(got[0].Service, "root"))
	// The elided marker records how many middle hops were dropped.
	qt.Check(t, qt.Equals(got[1].Elided, 12-1-(MaxChainHops-2)))
	// The most recent hop survives.
	qt.Check(t, qt.Equals(got[len(got)-1].Service, "latest"))
}

func TestBoundChain_ShortChainUnchanged(t *testing.T) {
	chain := []Hop{{Service: "a"}, {Service: "b"}}
	got := boundChain(chain)

	qt.Assert(t, qt.Equals(len(got), 2))
	qt.Check(t, qt.Equals(got[1].Service, "b"))
}
