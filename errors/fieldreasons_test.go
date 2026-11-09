package errors

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// panicOf runs f and returns what it panicked with as text, and whether it panicked.
func panicOf(f func()) (msg string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			msg, panicked = fmt.Sprint(r), true
		}
	}()

	f()

	return "", false
}

func TestPublic_ForwardsOnlyReasonWordsTheKitKnows(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	known := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 15, Reason: "required"}))
	qt.Check(t, qt.DeepEquals(known.Public().Errors, FieldErrors{{Field: 15, Reason: "required"}}))

	for name, list := range map[string][]FieldError{
		"a word naming something inside":   {{Field: 16, Reason: "pg_main_db_refused"}},
		"a word with a port in it":         {{Field: 17, Reason: "svc_cluster_local_8080"}},
		"an unknown word beside a known":   {{Field: 15, Reason: "required"}, {Field: 16, Reason: "svc_cluster_local_8080"}},
		"a well formed word nobody taught": {{Field: 16, Reason: "too_long"}},
		"a known word first, unknown last": {{Field: 1, Reason: "required"}, {Field: 2, Reason: "required"}, {Field: 3, Reason: "unheard_of"}},
	} {
		p := NewProblem("err:form:invalid", WithFieldErrors(list...))

		// The whole list stays internal: nothing is trimmed to its known words.
		qt.Check(t, qt.HasLen(p.Public().Errors, 0), qt.Commentf(name))

		public, err := json.Marshal(p.Public())
		qt.Assert(t, qt.IsNil(err), qt.Commentf(name))
		qt.Check(t, qt.Not(qt.StringContains(string(public), `"errors"`)), qt.Commentf("%s: %s", name, public))

		// The internal envelope carries any well-formed word, as before.
		internal, err := json.Marshal(p)
		qt.Assert(t, qt.IsNil(err), qt.Commentf(name))
		qt.Check(t, qt.StringContains(string(internal), list[len(list)-1].Reason), qt.Commentf("%s: %s", name, internal))
	}
}

func TestRegisterFieldReason_TeachesThePublicProjection(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	p := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 16, Reason: "too_long"}))
	other := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 16, Reason: "too_short"}))

	qt.Check(t, qt.HasLen(p.Public().Errors, 0))

	RegisterFieldReason("too_long")

	qt.Check(t, qt.DeepEquals(p.Public().Errors, FieldErrors{{Field: 16, Reason: "too_long"}}))
	// Teaching one word teaches no other.
	qt.Check(t, qt.HasLen(other.Public().Errors, 0))
}

func TestRegisterFieldReason_AWordThatIsNotAShortStableWordPanics(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	for name, word := range map[string]string{
		"empty":          "",
		"capitals":       "Too_Long",
		"a space":        "too long",
		"starts a digit": "1st",
		"33 characters":  "a" + strings.Repeat("b", 32),
		"a line break":   "required\n",
		"a sentence":     "the order field is empty",
		"a dash":         "too-long",
		"not ascii":      "tоo_long",
	} {
		msg, panicked := panicOf(func() { RegisterFieldReason(word) })
		qt.Check(t, qt.IsTrue(panicked), qt.Commentf(name))
		qt.Check(t, qt.StringContains(msg, "is not a short stable word"), qt.Commentf(name))
	}

	// A refused registration leaves the set as it was: the built-in word still passes.
	p := NewProblem("err:form:invalid")
	p.Errors = FieldErrors{{Field: 1, Reason: "required"}}
	qt.Check(t, qt.HasLen(p.Public().Errors, 1))
}

// TestPublicProblem_ABuiltByHandIsOnlyShapeChecked pins the limit the docs state: the known
// words are applied by Problem.Public. A PublicProblem built by hand is a plain data shape,
// checked for shape when written (as its Detail is not checked at all), so a service that
// builds one skips the word check and says so by doing it.
func TestPublicProblem_ABuiltByHandIsOnlyShapeChecked(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	byHand := PublicProblem{
		Title: "t", Status: 400, Code: "err:form:invalid",
		Errors: FieldErrors{{Field: 15, Reason: "pg_main_db_refused"}},
	}

	body, err := json.Marshal(byHand)
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(body), `"reason":"pg_main_db_refused"`))

	// The projection of a Problem holding the same list keeps it out.
	p := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 15, Reason: "pg_main_db_refused"}))
	qt.Check(t, qt.HasLen(p.Public().Errors, 0))
}

func TestRegisterFieldReason_TwiceIsHarmlessAndTheBuiltInStays(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	RegisterFieldReason("required")
	RegisterFieldReason("too_long")
	RegisterFieldReason("too_long")

	p := NewProblem("err:form:invalid",
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}, FieldError{Field: 16, Reason: "too_long"}),
	)
	qt.Check(t, qt.HasLen(p.Public().Errors, 2))

	resetFieldReasons()
	qt.Check(t, qt.HasLen(p.Public().Errors, 0))
	qt.Check(t, qt.HasLen(NewProblem("x", WithFieldErrors(FieldError{Field: 1, Reason: "required"})).Public().Errors, 1))
}

func TestRegisterFieldReason_IsSafeWhileRendering(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	p := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 16, Reason: "too_long"}))

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(2)

		go func(i int) {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				RegisterFieldReason(fmt.Sprintf("word_%d_%d", i, j))
			}
		}(i)

		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				_ = p.Public()
				_ = forwardableFieldErrors(p.Errors)
			}
		}()
	}

	wg.Wait()
}

// renderReasons runs one request through the renderer and returns the body and the log lines.
func renderReasons(t *testing.T, public bool, fail func(ctx *azugo.Context)) (string, *observer.ObservedLogs) {
	t.Helper()

	app := azugo.NewTestApp()
	app.AppName = "an-edge"
	app.RouterOptions().ErrorHandler = Handler(app.AppName, public, nil)
	app.Get("/boom", fail)
	app.Start(t)

	defer app.Stop()

	obs, logs := observer.New(zap.InfoLevel)
	qt.Assert(t, qt.IsNil(app.ReplaceLogger(zap.New(obs))))

	resp, err := app.TestClient().Get("/boom")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	return string(body), logs
}

// TestHandler_ARelayedUnknownWordStaysInsideAtThePublicEdgeAndIsLogged is the measured case:
// a downstream's well-formed but careless words, relayed through an edge.
func TestHandler_ARelayedUnknownWordStaysInsideAtThePublicEdgeAndIsLogged(t *testing.T) {
	t.Cleanup(resetFieldReasons)

	downstream := `{"title":"A required field has no value","status":400,"code":"err:form:fieldRequired",` +
		`"detail":"an internal sentence","source":"a-downstream",` +
		`"errors":[{"field":15,"reason":"required"},{"field":16,"reason":"pg_main_db_refused"}]}`

	relay := func(ctx *azugo.Context) {
		down, ok := ParseProblem([]byte(downstream))
		qt.Assert(t, qt.IsTrue(ok))
		ctx.Error(Relay(down, "an-edge", down.Status))
	}

	// At the public edge: no list, the rest of the error as before, one log line saying so.
	body, logs := renderReasons(t, true, relay)
	qt.Check(t, qt.Not(qt.StringContains(body, `"errors"`)), qt.Commentf(body))
	qt.Check(t, qt.Not(qt.StringContains(body, "pg_main_db_refused")), qt.Commentf(body))
	qt.Check(t, qt.StringContains(body, `"code":"err:form:fieldRequired"`))

	lines := logs.FilterMessage("request error").All()
	qt.Assert(t, qt.HasLen(lines, 1))
	qt.Check(t, qt.Equals(lines[0].ContextMap()["error.field_errors_dropped"], any(true)))

	// Inside, the same relayed list is carried whole and nothing is noted.
	body, logs = renderReasons(t, false, relay)
	qt.Check(t, qt.StringContains(body, `"reason":"pg_main_db_refused"`), qt.Commentf(body))

	lines = logs.FilterMessage("request error").All()
	qt.Assert(t, qt.HasLen(lines, 1))

	_, noted := lines[0].ContextMap()["error.field_errors_dropped"]
	qt.Check(t, qt.IsFalse(noted))

	// Once the edge is taught the word, it goes out.
	RegisterFieldReason("pg_main_db_refused")

	body, logs = renderReasons(t, true, relay)
	qt.Check(t, qt.StringContains(body, `"reason":"pg_main_db_refused"`), qt.Commentf(body))

	lines = logs.FilterMessage("request error").All()
	qt.Assert(t, qt.HasLen(lines, 1))

	_, noted = lines[0].ContextMap()["error.field_errors_dropped"]
	qt.Check(t, qt.IsFalse(noted))
}
