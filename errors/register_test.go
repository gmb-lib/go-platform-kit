package errors

import (
	"testing"

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

func TestRegisterReason_CollisionWithBuiltinPanics(t *testing.T) {
	t.Cleanup(resetRegistry)

	defer func() {
		qt.Check(t, qt.IsNotNil(recover()))
	}()

	RegisterReason("notFound", ReasonSpec{Status: fasthttp.StatusTeapot, Title: "nope"})
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
