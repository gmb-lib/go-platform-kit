package broker_test

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/gmb-lib/go-platform-kit/broker"
)

// Background work has no request to inherit correlation from. That is an ordinary
// state, not a fault: Stamp must still do the two things the event cannot go
// without — identify it, and strip anything token-shaped off its attributes.
//
// It matters because the alternative is what services actually did: a caller who
// cannot call Stamp writes its own, and the stripping is the half that gets lost.
func TestStampWithoutARequest(t *testing.T) {
	ev := &broker.Envelope{
		EventType: "document.retention_swept",
		Outcome:   broker.OutcomeSuccess,
		Attributes: map[string]any{
			"erased":        4,
			"authorization": "Bearer something",
		},
	}

	broker.Stamp(nil, ev)

	qt.Assert(t, qt.Not(qt.Equals(ev.EventID, "")))
	qt.Assert(t, qt.IsFalse(ev.OccurredAt.IsZero()))
	qt.Assert(t, qt.Equals(ev.CorrelationID, ""))
	qt.Assert(t, qt.Equals(ev.TraceID, ""))

	_, leaked := ev.Attributes["authorization"]
	qt.Assert(t, qt.IsFalse(leaked))
	qt.Assert(t, qt.Equals(ev.Attributes["erased"], any(4)))
}

// A stamped background event passes the same validation a published one must.
func TestStampWithoutARequestValidates(t *testing.T) {
	ev := &broker.Envelope{
		EventType:  "document.retention_swept",
		Categories: []broker.Category{broker.CategorySecurity},
		Outcome:    broker.OutcomeSuccess,
	}

	broker.Stamp(nil, ev)

	qt.Assert(t, qt.IsNil(ev.Validate()))
}
