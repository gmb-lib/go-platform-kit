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
	qt.Check(t, qt.Equals(propagation.CorrelationID(nil), ""))
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
