package errors

import (
	stderrors "errors"
	"testing"

	"azugo.io/core/http"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
)

func TestRegisterReason_DerivesStatusAndTitle(t *testing.T) {
	t.Cleanup(resetRegistry)
	RegisterReason("proofInvalid", ReasonSpec{Status: fasthttp.StatusBadRequest, Title: "Invalid proof"})

	p := NewProblem("err:credential:proofInvalid")

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusBadRequest))
	qt.Check(t, qt.Equals(p.Title, "Invalid proof"))
}

// TestRegisterReason_AllEntryPointsAgree is the regression guard for the
// divergence risk RegisterReason exists to remove: NewProblem, FromResultCode,
// and HTTP must all derive the same status for a registered code, since a
// service may reach the taxonomy through any of the three.
func TestRegisterReason_AllEntryPointsAgree(t *testing.T) {
	t.Cleanup(resetRegistry)
	RegisterReason("proofInvalid", ReasonSpec{Status: fasthttp.StatusBadRequest, Title: "Invalid proof"})

	fromNewProblem := NewProblem("err:credential:proofInvalid").Status

	fromResultCode, ok := FromResultCode("err:credential:proofInvalid").(interface{ StatusCode() int })
	qt.Assert(t, qt.IsTrue(ok))

	fromHTTP, ok := HTTP("credential", "proofInvalid").(interface{ StatusCode() int })
	qt.Assert(t, qt.IsTrue(ok))

	qt.Check(t, qt.Equals(fromNewProblem, fasthttp.StatusBadRequest))
	qt.Check(t, qt.Equals(fromResultCode.StatusCode(), fasthttp.StatusBadRequest))
	qt.Check(t, qt.Equals(fromHTTP.StatusCode(), fasthttp.StatusBadRequest))
}

func TestRegisterReason_SafeMessage(t *testing.T) {
	t.Cleanup(resetRegistry)
	RegisterReason("signingCertUnavailable", ReasonSpec{Status: fasthttp.StatusInternalServerError, Title: "Internal server error"})

	err := FromResultCode("err:mint:signingCertUnavailable")

	se, ok := err.(interface{ SafeError() string })
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(se.SafeError(), "Internal server error"))

	// An explicit safe message overrides the registered title, same as the
	// built-in reasons' resource-label override.
	err = FromResultCode("err:mint:signingCertUnavailable", "signing certificate temporarily unavailable")
	se, ok = err.(interface{ SafeError() string })
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(se.SafeError(), "signing certificate temporarily unavailable"))
}

// TestRegisterReason_CollisionWithBuiltinPanicsWithoutOptIn guards the
// fail-fast half of the shadowing contract: registering a reason whose
// normalized form collides with a built-in reason panics unless the call
// passes AllowBuiltinOverride(), and the panic message points the caller at
// that option so an accidental collision is self-diagnosing at startup.
func TestRegisterReason_CollisionWithBuiltinPanicsWithoutOptIn(t *testing.T) {
	t.Cleanup(resetRegistry)

	defer func() {
		r := recover()
		qt.Assert(t, qt.IsNotNil(r))

		msg, ok := r.(string)
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf("panic value should be a string, got %T", r))
		qt.Check(t, qt.StringContains(msg, "AllowBuiltinOverride"))
	}()

	RegisterReason("notFound", ReasonSpec{Status: fasthttp.StatusTeapot, Title: "nope"})
}

// TestRegisterReason_CollisionWithBuiltinSucceedsWithOptIn guards the opt-in
// half: the same colliding registration succeeds when the call passes
// AllowBuiltinOverride(), making a deliberate shadowing of a built-in reason
// possible — and auditable at the call site.
func TestRegisterReason_CollisionWithBuiltinSucceedsWithOptIn(t *testing.T) {
	t.Cleanup(resetRegistry)

	RegisterReason("notFound", ReasonSpec{Status: fasthttp.StatusTeapot, Title: "nope"}, AllowBuiltinOverride())

	spec, ok := lookupReason("notFound")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(spec.Status, fasthttp.StatusTeapot))
	qt.Check(t, qt.Equals(spec.Title, "nope"))
}

func TestRegisterReason_ReRegisteringCustomReasonDoesNotPanic(t *testing.T) {
	t.Cleanup(resetRegistry)

	RegisterReason("proofInvalid", ReasonSpec{Status: fasthttp.StatusBadRequest, Title: "Invalid proof"})
	RegisterReason("proofInvalid", ReasonSpec{Status: fasthttp.StatusUnprocessableEntity, Title: "Invalid proof"})

	p := NewProblem("err:credential:proofInvalid")
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusUnprocessableEntity))
}

func TestRegisterReason_UnregisteredReasonStillFallsBackTo500(t *testing.T) {
	t.Cleanup(resetRegistry)

	p := NewProblem("err:credential:someUnregisteredReason")
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusInternalServerError))
}

// TestRegisterReason_RendersFullEnvelopeThroughHandler is the acceptance test
// for the render path: a registered reason surfaced via FromResultCode/HTTP +
// ctx.Error must render the same code and title as NewProblem(code), not a
// genericized err:request:* code with a status-derived title. registeredError
// carries the code (Coder) precisely so toProblem preserves it here — this is
// the wire-level half of the AllEntryPointsAgree guarantee.
func TestRegisterReason_RendersFullEnvelopeThroughHandler(t *testing.T) {
	t.Cleanup(resetRegistry)
	RegisterReason("proofInvalid", ReasonSpec{Status: fasthttp.StatusBadRequest, Title: "Invalid proof"})

	// FromResultCode → ctx.Error path.
	p := toProblem(FromResultCode("err:credential:proofInvalid"))
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusBadRequest))
	qt.Check(t, qt.Equals(p.Code, "err:credential:proofInvalid"))
	qt.Check(t, qt.Equals(p.Title, "Invalid proof"))

	// HTTP(domain, reason) → ctx.Error path renders identically.
	p = toProblem(HTTP("credential", "proofInvalid"))
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusBadRequest))
	qt.Check(t, qt.Equals(p.Code, "err:credential:proofInvalid"))
	qt.Check(t, qt.Equals(p.Title, "Invalid proof"))
}

func TestRegisterReason_OutOfRangeStatusPanics(t *testing.T) {
	t.Cleanup(resetRegistry)

	defer func() {
		qt.Check(t, qt.IsNotNil(recover()))
	}()

	RegisterReason("proofInvalid", ReasonSpec{Status: 0, Title: "Invalid proof"})
}

// TestRegisterReason_TakesPrecedenceOverCollidingBuiltin is the regression
// guard for a deliberate design decision: registered reasons take precedence
// over the built-in taxonomy, so a reason registered (with the explicit
// AllowBuiltinOverride opt-in) under a name that collides with a built-in
// bucket after normalization must win over that bucket. mapReason (and the
// title lookup behind NewProblem) consult the registry before the built-in
// switch, so a registered "not-found" renders its own status/title rather
// than the built-in "Not found"/404 bucket — separator-insensitivity (see
// normalize) holds through the registry path too: "not-found" (registered)
// and "not_found" (looked up) collide to the same key.
func TestRegisterReason_TakesPrecedenceOverCollidingBuiltin(t *testing.T) {
	t.Cleanup(resetRegistry)
	RegisterReason("not-found", ReasonSpec{Status: fasthttp.StatusTeapot, Title: "Registry entry not found"}, AllowBuiltinOverride())

	err := FromResultCode("err:registry:not_found")

	var registered registeredError
	ok := stderrors.As(err, &registered)
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("expected a registeredError for a reason registered over a built-in collision, got %T: %v", err, err))
	qt.Check(t, qt.Equals(registered.ErrorCode(), "err:registry:not_found"))
	qt.Check(t, qt.Equals(registered.StatusCode(), fasthttp.StatusTeapot))
	qt.Check(t, qt.Equals(registered.SafeError(), "Registry entry not found"))

	// HTTP(domain, reason) agrees.
	var httpErr registeredError
	ok = stderrors.As(HTTP("registry", "not_found"), &httpErr)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(httpErr.StatusCode(), fasthttp.StatusTeapot))

	// NewProblem must derive the same status and title — the entry points
	// agree by construction (both flow through mapReason / titleForReasonOK).
	p := NewProblem("err:registry:not_found")
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusTeapot))
	qt.Check(t, qt.Equals(p.Title, "Registry entry not found"))
}

// TestRegisterReason_UnregisteredCollidingReasonStillUsesBuiltin is the
// companion regression: a domain that never registered anything for
// "not_found" (or any other built-in-colliding word) must still get the
// built-in bucket — the precedence fix only changes behavior for a reason
// actually registered, never for the default, unregistered case.
func TestRegisterReason_UnregisteredCollidingReasonStillUsesBuiltin(t *testing.T) {
	t.Cleanup(resetRegistry)

	err := FromResultCode("err:somedomain:not_found")
	var nf http.NotFoundError
	ok := stderrors.As(err, &nf)
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("expected the built-in NotFoundError for an unregistered reason, got %T: %v", err, err))
	qt.Check(t, qt.Equals(nf.StatusCode(), fasthttp.StatusNotFound))

	var notFoundError http.NotFoundError
	ok = stderrors.As(HTTP("somedomain", "not_found"), &notFoundError)
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("HTTP(domain, reason) must agree with FromResultCode"))
}

// TestRegisterReason_UnregisteredUnrecognizedReasonStillInternal is the other
// regression half: a reason matching neither the registry nor a built-in
// bucket must still fall to the safe, non-leaking InternalError fallback.
func TestRegisterReason_UnregisteredUnrecognizedReasonStillInternal(t *testing.T) {
	t.Cleanup(resetRegistry)

	err := FromResultCode("err:somedomain:teapot")
	var internalError InternalError
	ok := stderrors.As(err, &internalError)
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("expected InternalError for an unrecognized, unregistered reason, got %T: %v", err, err))

	var internalError2 InternalError
	ok = stderrors.As(HTTP("somedomain", "teapot"), &internalError2)
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("HTTP(domain, reason) must agree with FromResultCode"))
}
