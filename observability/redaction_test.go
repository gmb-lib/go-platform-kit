package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// newRedactingLogger builds a logger whose core is the redaction decorator over
// a JSON encoder writing to buf. It bakes the base service.name field once (as
// App.Log() does) and then re-applies it through the decorator (as
// App.ReplaceLogger does), so the test exercises the real dedupe path.
func newRedactingLogger(buf *bytes.Buffer) *zap.Logger {
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	inner := zapcore.NewCore(enc, zapcore.AddSync(buf), zapcore.DebugLevel)

	// Base field baked once on the inner core (simulates App.Log()).
	inner = inner.With([]zapcore.Field{zap.String("service.name", "document-svc")})

	rc := &redactCore{Core: inner, policy: DefaultRedactionPolicy()}

	// Re-apply the base field through the decorator (simulates ReplaceLogger).
	return zap.New(rc).With(zap.String("service.name", "document-svc"))
}

// str extracts a string from a decoded JSON value, returning "" for non-strings.
func str(v any) string {
	s, _ := v.(string)

	return s
}

func logLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	line := strings.TrimSpace(buf.String())
	qt.Assert(t, qt.Not(qt.Equals(line, "")))

	m := map[string]any{}
	qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(line), &m)))

	return m
}

func TestRedaction_DropsSecrets(t *testing.T) {
	buf := &bytes.Buffer{}
	log := newRedactingLogger(buf)

	log.Info("outbound call",
		zap.String("authorization", "Bearer secret-token"),
		zap.String("dpop_proof", "eyJ..."),
		zap.String("service_token", "abc"),
		zap.String("client_secret", "shh"),
		zap.String("file_data", "%PDF-1.7 ...bytes..."),
		zap.String("session_id", "s-1"),
	)

	m := logLine(t, buf)

	for _, k := range []string{"authorization", "dpop_proof", "service_token", "client_secret", "file_data", "session_id"} {
		_, present := m[k]
		qt.Check(t, qt.IsFalse(present), qt.Commentf("sensitive key %q must be dropped", k))
	}

	// And the raw secret value must not appear anywhere in the output.
	qt.Check(t, qt.IsFalse(strings.Contains(buf.String(), "secret-token")))
}

func TestRedaction_MasksPII(t *testing.T) {
	buf := &bytes.Buffer{}
	log := newRedactingLogger(buf)

	log.Info("subject access",
		zap.String("email", "person@example.com"),
		zap.String("given_name", "Jane"),
		zap.String("personal_code", "123456-78901"),
	)

	m := logLine(t, buf)

	qt.Check(t, qt.Equals(str(m["email"]), maskValue))
	qt.Check(t, qt.Equals(str(m["given_name"]), maskValue))
	qt.Check(t, qt.Equals(str(m["personal_code"]), maskValue))
	qt.Check(t, qt.IsFalse(strings.Contains(buf.String(), "person@example.com")))
	qt.Check(t, qt.IsFalse(strings.Contains(buf.String(), "123456-78901")))
}

func TestRedaction_KeepsSafeFields(t *testing.T) {
	buf := &bytes.Buffer{}
	log := newRedactingLogger(buf)

	log.Info("processed",
		zap.String("document_id", "doc-1"),
		zap.String("correlation_id", "01J..."),
		zap.Int("count", 3),
	)

	m := logLine(t, buf)

	qt.Check(t, qt.Equals(str(m["document_id"]), "doc-1"))
	qt.Check(t, qt.Equals(str(m["correlation_id"]), "01J..."))
	num, ok := m["count"].(float64)
	qt.Check(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(num, float64(3)))
}

func TestRedaction_DedupesBaseField(t *testing.T) {
	buf := &bytes.Buffer{}
	log := newRedactingLogger(buf)

	log.Info("hello")

	// service.name was baked once and re-applied once; it must appear exactly
	// once in the encoded line (no duplicate JSON key).
	count := strings.Count(buf.String(), `"service.name"`)
	qt.Check(t, qt.Equals(count, 1), qt.Commentf("service.name should appear once, got %d", count))

	m := logLine(t, buf)
	qt.Check(t, qt.Equals(str(m["service.name"]), "document-svc"))
}

func TestRedaction_ScrubsContextFields(t *testing.T) {
	// Fields added via With (e.g. ctx.AddLogFields) must also be scrubbed.
	buf := &bytes.Buffer{}
	log := newRedactingLogger(buf).With(zap.String("authorization", "Bearer x"))

	log.Info("with-context")

	m := logLine(t, buf)
	_, present := m["authorization"]
	qt.Check(t, qt.IsFalse(present), qt.Commentf("With-fields must be redacted too"))
}
