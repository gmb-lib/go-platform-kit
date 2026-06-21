package broker_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gmb-sig/go-platform-kit/broker"
)

// frozenEnvelopeKeys is the append-only contract for the event envelope: every
// JSON key path an Envelope (and its nested objects) commits to on the wire.
//
// The rule it enforces: new optional fields may be ADDED; existing fields and
// JSON keys are NEVER renamed or removed. Renaming or dropping a key breaks
// every already-emitted event and the audit hash-chain that canonicalises the
// envelope, so the change must stay additive.
//
// Maintenance: when you add a field to Envelope/Actor/Resource, add its key
// path here too (so it becomes protected from that point on). NEVER delete or
// edit an existing entry — that is exactly the breakage this test guards.
var frozenEnvelopeKeys = []string{
	"actor",
	"actor.assurance",
	"actor.id",
	"actor.type",
	"attributes",
	"category",
	"correlation_id",
	"data_subjects",
	"device",
	"event_id",
	"event_type",
	"hash",
	"ip",
	"lawful_basis",
	"occurred_at",
	"operation",
	"outcome",
	"prev_hash",
	"purpose",
	"resource",
	"resource.id",
	"resource.type",
	"trace_id",
}

// TestEnvelopeSchemaIsAppendOnly fails if the Envelope stops emitting any key it
// has committed to — i.e. a field removal or a json-tag rename. Adding a new
// field does not fail the test (additions are allowed); it just won't be
// protected until it is listed in frozenEnvelopeKeys.
func TestEnvelopeSchemaIsAppendOnly(t *testing.T) {
	got := jsonKeyPaths(reflect.TypeOf(broker.Envelope{}), "")

	for _, key := range frozenEnvelopeKeys {
		if !got[key] {
			t.Errorf("frozen envelope key %q is no longer emitted: a removal or json-tag rename "+
				"breaks wire compatibility with stored events and the audit hash-chain. The "+
				"envelope schema is append-only — only additive changes are allowed.", key)
		}
	}
}

// jsonKeyPaths returns the set of JSON key paths a value of type t marshals to.
// It descends into nested structs defined in the same package (dereferencing
// pointers and slice/map element types), joining nested keys with a dot — so
// Actor.ID surfaces as "actor.id". Types from other packages (e.g. time.Time,
// which marshals to a scalar) are treated as leaves and not descended into.
func jsonKeyPaths(t reflect.Type, prefix string) map[string]bool {
	out := map[string]bool{}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return out
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}

		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = true

		// Descend into nested struct types defined in this package.
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && ft.PkgPath() == t.PkgPath() {
			for k := range jsonKeyPaths(ft, path) {
				out[k] = true
			}
		}
	}

	return out
}
