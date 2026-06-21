// Package broker provides thin publish/consume wrappers that carry the frozen
// event envelope, stamp correlation/trace ids, and enforce the project
// conventions (TLS + per-topic ACLs, idempotent consume). The audit/security
// emitter libraries (go-eidas-audit, go-gdpr-audit, go-sec-events) build on
// these helpers; go-platform-kit itself emits nothing.
//
// Events carry actor/identity metadata + correlation only — never bearer
// tokens. The transport itself is abstracted behind the Transport interface so
// go-platform-kit stays in-process glue and is not coupled to a specific broker
// client.
package broker

import "time"

// Category classifies an event by audit regime; an event may belong to more than one.
type Category string

const (
	CategorySigning    Category = "signing"     // eIDAS-audit — signing evidence
	CategoryGDPRAccess Category = "gdpr_access" // GDPR-audit — personal-data access
	CategorySecurity   Category = "security"    // NIS2-audit — security telemetry
)

// Operation is the action an event records.
type Operation string

const (
	OpRead   Operation = "read"
	OpCreate Operation = "create"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
	OpExport Operation = "export"
	OpSign   Operation = "sign"
)

// Outcome is the result of the recorded action.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeDenied  Outcome = "denied"
)

// Actor identifies who (or which service) performed the action.
type Actor struct {
	// ID is the identity ref (user) or service identity.
	ID string `json:"id,omitempty"`
	// Type is "user" or "service".
	Type string `json:"type,omitempty"`
	// Assurance is the level of assurance, where relevant (e.g. "high").
	Assurance string `json:"assurance,omitempty"`
}

// Resource is the thing the event concerns (document/envelope/identity/…).
type Resource struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

// Envelope is the common JSON shape every event conforms to. By project rule it
// evolves append-only — new optional fields only; existing fields and JSON keys
// are never renamed or removed, so producers and consumers stay wire-compatible
// without a version gate (a schema test enforces the field set). go-platform-kit
// owns the envelope and the correlation it carries; the emitter libraries own its
// content.
type Envelope struct {
	// Identity & correlation.
	EventID       string    `json:"event_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	TraceID       string    `json:"trace_id,omitempty"`

	// Classification.
	Categories []Category `json:"category"`
	EventType  string     `json:"event_type"`

	// Subject of the event.
	Actor *Actor `json:"actor,omitempty"`
	// DataSubjects identify the people the event concerns, for subject
	// indexing. Values MUST be pseudonymous internal identity references (e.g.
	// the identity-record ULID) — NEVER national identifiers, personal codes,
	// names, or e-mail addresses. These values flow into broker streams, SIEM
	// logs, and audit stores; key-based redaction cannot catch identifying
	// values placed here.
	DataSubjects []string  `json:"data_subjects,omitempty"`
	Resource     *Resource `json:"resource,omitempty"`
	Operation    Operation `json:"operation,omitempty"`

	// GDPR access context (GDPR-audit).
	LawfulBasis string `json:"lawful_basis,omitempty"`
	Purpose     string `json:"purpose,omitempty"`

	// Result + pseudonymisable context.
	Outcome Outcome `json:"outcome"`
	IP      string  `json:"ip,omitempty"`
	Device  string  `json:"device,omitempty"`

	// Chained-sink integrity (set by the consuming sink, not the publisher).
	PrevHash string `json:"prev_hash,omitempty"`
	Hash     string `json:"hash,omitempty"`

	// Attributes are typed and must contain no free-text PII and no document
	// content. Bearer-token-shaped keys are stripped defensively on publish.
	Attributes map[string]any `json:"attributes,omitempty"`
}
