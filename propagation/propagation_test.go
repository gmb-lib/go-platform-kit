package propagation_test

import (
	"context"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/gmb-lib/go-platform-kit/propagation"
)

func TestWithAndReadCorrelationID_BackgroundCarrier(t *testing.T) {
	ctx := propagation.WithCorrelationID(context.Background(), "01ABC")

	qt.Check(t, qt.Equals(propagation.CorrelationID(ctx), "01ABC"))
}

func TestWithCorrelationID_EmptyIsNoOp(t *testing.T) {
	base := context.Background()
	ctx := propagation.WithCorrelationID(base, "")

	qt.Check(t, qt.Equals(ctx, base))
	qt.Check(t, qt.Equals(propagation.CorrelationID(ctx), ""))
}

func TestCorrelationID_NilAndAbsent(t *testing.T) {
	qt.Check(t, qt.Equals(propagation.CorrelationID(nil), "")) //nolint:staticcheck // deliberately passing a nil Context to assert nil-safety
	qt.Check(t, qt.Equals(propagation.CorrelationID(context.Background()), ""))
}

// TestCorrelationID_RequestValueCarrier proves the accessor resolves the id the
// inbound web middleware stores under the request value name — the path a
// framework-free client relies on to read an id it never set itself.
func TestCorrelationID_RequestValueCarrier(t *testing.T) {
	//nolint:staticcheck // exercising the string request-value carrier deliberately.
	ctx := context.WithValue(context.Background(), requestValueNameForTest(), "01REQ")

	qt.Check(t, qt.Equals(propagation.CorrelationID(ctx), "01REQ"))
}

// TestBackgroundCarrierWins verifies the explicit background id takes precedence
// over a request value, so a job that sets its own id is authoritative.
func TestBackgroundCarrierWins(t *testing.T) {
	//nolint:staticcheck // seeding the request-value carrier for the precedence check.
	ctx := context.WithValue(context.Background(), requestValueNameForTest(), "01REQ")
	ctx = propagation.WithCorrelationID(ctx, "01BG")

	qt.Check(t, qt.Equals(propagation.CorrelationID(ctx), "01BG"))
}

// requestValueNameForTest returns the string key the middleware uses, as a
// non-constant so the test's context.WithValue does not trip the vet check that
// (correctly) discourages string keys in production code.
func requestValueNameForTest() any {
	return propagation.RequestValueName()
}

// The app instance id gets the same five cases as the correlation id above: it
// travels the same two carriers, and the reason this accessor exists at all is
// that a client holding only a context.Context has to be able to forward the id
// across an internal hop. An accessor that silently returned "" would make the
// id stop at the edge service, which is the failure this package prevents.

func TestWithAndReadAppInstanceID_BackgroundCarrier(t *testing.T) {
	ctx := propagation.WithAppInstanceID(context.Background(), "instance-xyz")

	qt.Check(t, qt.Equals(propagation.AppInstanceID(ctx), "instance-xyz"))
}

func TestWithAppInstanceID_EmptyIsNoOp(t *testing.T) {
	base := context.Background()
	ctx := propagation.WithAppInstanceID(base, "")

	qt.Check(t, qt.Equals(ctx, base))
	qt.Check(t, qt.Equals(propagation.AppInstanceID(ctx), ""))
}

func TestAppInstanceID_NilAndAbsent(t *testing.T) {
	qt.Check(t, qt.Equals(propagation.AppInstanceID(nil), "")) //nolint:staticcheck // deliberately passing a nil Context to assert nil-safety
	qt.Check(t, qt.Equals(propagation.AppInstanceID(context.Background()), ""))
}

// TestAppInstanceID_RequestValueCarrier proves the accessor resolves the id the
// inbound web middleware stores under the request value name — the path an
// outbound client with no request object relies on to forward an id it never
// set itself.
func TestAppInstanceID_RequestValueCarrier(t *testing.T) {
	//nolint:staticcheck // exercising the string request-value carrier deliberately.
	ctx := context.WithValue(context.Background(), appInstanceValueNameForTest(), "instance-req")

	qt.Check(t, qt.Equals(propagation.AppInstanceID(ctx), "instance-req"))
}

// TestAppInstanceBackgroundCarrierWins verifies the explicit background id takes
// precedence over a request value, so a job that sets its own id is
// authoritative — the same precedence the correlation id has.
func TestAppInstanceBackgroundCarrierWins(t *testing.T) {
	//nolint:staticcheck // seeding the request-value carrier for the precedence check.
	ctx := context.WithValue(context.Background(), appInstanceValueNameForTest(), "instance-req")
	ctx = propagation.WithAppInstanceID(ctx, "instance-bg")

	qt.Check(t, qt.Equals(propagation.AppInstanceID(ctx), "instance-bg"))
}

// TestAppInstanceAndCorrelationIDsAreIndependent pins that the two ids do not
// share a carrier: binding one must not resolve as the other, which a copy-paste
// of the wrong value name would silently do.
func TestAppInstanceAndCorrelationIDsAreIndependent(t *testing.T) {
	ctx := propagation.WithCorrelationID(context.Background(), "01CORR")
	ctx = propagation.WithAppInstanceID(ctx, "instance-xyz")

	qt.Check(t, qt.Equals(propagation.CorrelationID(ctx), "01CORR"))
	qt.Check(t, qt.Equals(propagation.AppInstanceID(ctx), "instance-xyz"))
	qt.Check(t, qt.Not(qt.Equals(propagation.RequestValueName(), propagation.AppInstanceRequestValueName())))
}

// appInstanceValueNameForTest mirrors requestValueNameForTest for the app
// instance id's request-value carrier.
func appInstanceValueNameForTest() any {
	return propagation.AppInstanceRequestValueName()
}
