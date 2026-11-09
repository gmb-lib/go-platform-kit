package errors

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"sync"
)

// MaxFieldErrors bounds the list of fields a Problem may name. A list longer than
// this is dropped whole, never cut to length.
const MaxFieldErrors = 50

// maxFieldNumber is the largest field number a FieldError may carry: the identifier
// is a positive 32-bit integer.
const maxFieldNumber = math.MaxInt32

// fieldReasonPattern is the whole of what a reason may be: a short stable word.
var fieldReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// FieldError names one field of the caller's own request that is at fault, and why,
// as data a client can act on without parsing prose ([IETF RFC 9457 §3.2] allows a
// problem type to extend the object with members; [IETF RFC 9457 §3.1.4] says a client
// should not parse "detail").
//
// A FieldError carries no free text, on purpose: Field is the number the request
// already uses to address the field and Reason is a short stable word (lower case
// letters, digits and underscores, 1 to 32 characters, starting with a letter). That
// keeps free text out of the list. A well-formed word can still name something inside a
// service, so the public projection goes one step further and forwards only reason
// words the kit knows (see RegisterFieldReason).
type FieldError struct {
	// Field is the number of the field at fault, from 1 to 2147483647.
	Field int `json:"field"`
	// Reason is a short stable word saying what is wrong with it, such as "required".
	Reason string `json:"reason"`
}

// FieldErrors is the list a Problem carries in its errors member. The rules for a list
// (see FieldError, MaxFieldErrors) are enforced by this type's own JSON methods, so any
// encoder or decoder that handles a Problem applies them: the renderer, ParseProblem,
// encoding/json and the web framework's own encoder alike. A list that breaks a rule is
// written as null and read as no list at all; it is never trimmed or partly kept. The
// methods sit on this type and not on Problem on purpose: a method on Problem would be
// promoted onto every type that embeds one and would silently stop that type's other
// fields from being written or read.
type FieldErrors []FieldError

// MarshalJSON writes the list, or null when it breaks a rule.
func (l FieldErrors) MarshalJSON() ([]byte, error) {
	if !validFieldErrors(l) {
		return []byte("null"), nil
	}

	return json.Marshal([]FieldError(l))
}

// UnmarshalJSON reads the list strictly and never fails: anything but an array of objects
// holding exactly a field and a reason, or a list that breaks a rule, leaves no list.
func (l *FieldErrors) UnmarshalJSON(data []byte) error {
	list, _ := decodeFieldErrors(data)
	*l = list

	return nil
}

// validFieldErrors reports whether list may be shown: at most MaxFieldErrors entries,
// each field in range, each reason a short stable word, no entry repeated. An empty
// list is valid and means there is nothing to show.
func validFieldErrors(list FieldErrors) bool {
	if len(list) > MaxFieldErrors {
		return false
	}

	seen := make(map[FieldError]struct{}, len(list))

	for _, e := range list {
		if e.Field < 1 || e.Field > maxFieldNumber {
			return false
		}

		if !fieldReasonPattern.MatchString(e.Reason) {
			return false
		}

		if _, repeated := seen[e]; repeated {
			return false
		}

		seen[e] = struct{}{}
	}

	return true
}

// cleanFieldErrors returns a copy of list when it is valid (nil when it is empty), and
// ok=false when it must be dropped. A list is never repaired or partly kept: one bad
// entry and the whole list goes.
func cleanFieldErrors(list FieldErrors) (clean FieldErrors, ok bool) {
	if len(list) == 0 {
		return nil, true
	}

	if !validFieldErrors(list) {
		return nil, false
	}

	return slices.Clone(list), true
}

// decodeFieldErrors reads the errors member strictly: anything but an array of objects
// holding exactly a field and a reason is dropped whole, so a downstream that sends
// something else cannot get it relayed. ok is false when a list was dropped.
func decodeFieldErrors(raw json.RawMessage) (FieldErrors, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var list []FieldError
	if err := dec.Decode(&list); err != nil {
		return nil, false
	}

	return cleanFieldErrors(list)
}

// WithFieldErrors names the fields of the request at fault, so a client can read them
// as data (see FieldError). It replaces any list already set. A list that is not valid
// is dropped whole and the problem is rendered without it; the renderer notes that on
// the error's log line so the mistake is not silent.
func WithFieldErrors(list ...FieldError) ProblemOption {
	return func(p *Problem) {
		clean, ok := cleanFieldErrors(list)
		p.Errors = clean
		p.fieldErrorsDropped = !ok
	}
}

var (
	publicReasonsMu sync.RWMutex
	// publicReasons is the set of reason words the public projection may forward. Only the
	// word the first condition needs is built in; a service teaches the edge any other.
	publicReasons = map[string]struct{}{"required": {}}
)

// RegisterFieldReason teaches the public projection a reason word it may forward to a
// client. A downstream's reason words travel on the internal envelope as long as they are
// well formed, but the public projection (Problem.Public, which PublicErrors renders)
// forwards a list only when every word in it is one the kit knows, so a word that names
// something inside the service never reaches a client. "required" is built in; call this
// once at start-up, in the service that renders the public envelope, for each other word.
// It panics on a word that is not a short stable word (see FieldError), as a start-up
// mistake should; registering a word twice is harmless. The set is process-wide, like the
// one RegisterReason keeps, and cannot be read or shrunk by a consumer: register from the
// one place the service starts, never from a package's init, which would widen what every
// edge linked with it forwards.
func RegisterFieldReason(word string) {
	if !fieldReasonPattern.MatchString(word) {
		panic("errors: field reason " + strconv.Quote(word) + " is not a short stable word (^[a-z][a-z0-9_]{0,31}$)")
	}

	publicReasonsMu.Lock()
	defer publicReasonsMu.Unlock()

	publicReasons[word] = struct{}{}
}

// resetFieldReasons returns the set to the built-in word. Test-only: without it a test
// that registers a word leaks it into every later test in the package.
func resetFieldReasons() {
	publicReasonsMu.Lock()
	defer publicReasonsMu.Unlock()

	publicReasons = map[string]struct{}{"required": {}}
}

// forwardableFieldErrors reports whether every reason in list is a word the public
// projection may forward. An empty list has nothing to forward and passes.
func forwardableFieldErrors(list FieldErrors) bool {
	publicReasonsMu.RLock()
	defer publicReasonsMu.RUnlock()

	for _, e := range list {
		if _, known := publicReasons[e.Reason]; !known {
			return false
		}
	}

	return true
}

// decodeProblem reads a problem body into p, noting when a field list was dropped so the
// renderer can say so on the error's log line. The rest of the problem is kept either way,
// so a downstream's error still reaches the caller.
func decodeProblem(data []byte, p *Problem) error {
	type plain Problem

	// The outer Errors shadows the embedded one, so the list arrives raw and is read here.
	shadow := struct {
		*plain
		Errors json.RawMessage `json:"errors"`
	}{plain: (*plain)(p)}

	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}

	if len(shadow.Errors) == 0 || bytes.Equal(shadow.Errors, []byte("null")) {
		return nil
	}

	list, ok := decodeFieldErrors(shadow.Errors)
	p.Errors = list

	if !ok {
		p.fieldErrorsDropped = true
	}

	return nil
}
