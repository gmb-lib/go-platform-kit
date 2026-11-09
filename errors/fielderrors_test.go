package errors

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
)

const requiredFifteen = `"errors":[{"field":15,"reason":"required"}]`

func TestWithFieldErrors_CarriesTheListInOrder(t *testing.T) {
	p := NewProblem("err:form:invalid",
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}, FieldError{Field: 7, Reason: "required"}),
	)

	body, err := json.Marshal(p)
	qt.Assert(t, qt.IsNil(err))
	// The list is written as given: the kit never reorders or repairs it.
	qt.Check(t, qt.StringContains(string(body), `"errors":[{"field":15,"reason":"required"},{"field":7,"reason":"required"}]`))
	qt.Check(t, qt.IsFalse(p.fieldErrorsDropped))
}

func TestFieldErrors_AbsentWhenThereAreNone(t *testing.T) {
	for name, p := range map[string]*Problem{
		"never set":      NewProblem("err:form:invalid"),
		"set to nothing": NewProblem("err:form:invalid", WithFieldErrors()),
	} {
		body, err := json.Marshal(p)
		qt.Assert(t, qt.IsNil(err), qt.Commentf(name))
		qt.Check(t, qt.Not(qt.StringContains(string(body), `"errors"`)), qt.Commentf(name))
		qt.Check(t, qt.IsFalse(p.fieldErrorsDropped), qt.Commentf(name))
	}
}

func TestWithFieldErrors_ReplacesTheListAlreadySet(t *testing.T) {
	p := NewProblem("err:form:invalid",
		WithFieldErrors(FieldError{Field: 1, Reason: "required"}),
		WithFieldErrors(FieldError{Field: 2, Reason: "required"}),
	)

	qt.Check(t, qt.DeepEquals(p.Errors, FieldErrors{{Field: 2, Reason: "required"}}))
}

func TestWithFieldErrors_TheDropNoteDoesNotStick(t *testing.T) {
	p := NewProblem("err:form:invalid",
		WithFieldErrors(FieldError{Field: 0, Reason: "required"}),
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}),
	)

	// The bad list was replaced by a good one that is sent, so nothing was dropped at the end.
	qt.Check(t, qt.DeepEquals(p.Errors, FieldErrors{{Field: 15, Reason: "required"}}))
	qt.Check(t, qt.IsFalse(p.fieldErrorsDropped))
}

func TestWithFieldErrors_HoldsACopyOfTheCallersSlice(t *testing.T) {
	mine := []FieldError{{Field: 15, Reason: "required"}}
	p := NewProblem("err:form:invalid", WithFieldErrors(mine...))

	mine[0].Reason = "changed"

	qt.Check(t, qt.Equals(p.Errors[0].Reason, "required"))
}

func TestPublic_KeepsTheFieldListAndStillDropsTopology(t *testing.T) {
	p := NewProblem("err:form:invalid",
		WithSource("a-service"),
		WithDetail("internal only detail"),
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}),
	)
	p.TraceID = "01TRACE"
	p.Chain = []Hop{{Service: "a-service", Code: p.Code, Status: p.Status}}

	body, err := json.Marshal(p.Public())
	qt.Assert(t, qt.IsNil(err))

	s := string(body)
	qt.Check(t, qt.StringContains(s, requiredFifteen))
	qt.Check(t, qt.Not(qt.StringContains(s, "source")), qt.Commentf("public body leaked source: %s", s))
	qt.Check(t, qt.Not(qt.StringContains(s, "chain")), qt.Commentf("public body leaked chain: %s", s))
	qt.Check(t, qt.Not(qt.StringContains(s, "internal only detail")), qt.Commentf("public body leaked detail: %s", s))
}

// TestPublic_ChecksTheListOnItsOwn: a Go caller that reads the projection's list directly,
// without marshalling it, never gets one that breaks a rule.
func TestPublic_ChecksTheListOnItsOwn(t *testing.T) {
	p := NewProblem("err:form:invalid")
	p.Errors = FieldErrors{{Field: 15, Reason: "required"}, {Field: 16, Reason: "Not A Word"}}

	qt.Check(t, qt.HasLen(p.Public().Errors, 0))
}

func TestPublic_HoldsACopyOfTheList(t *testing.T) {
	p := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 15, Reason: "required"}))

	pp := p.Public()
	pp.Errors[0].Reason = "changed"

	qt.Check(t, qt.Equals(p.Errors[0].Reason, "required"))
}

func TestRelay_KeepsTheFieldListAsACopy(t *testing.T) {
	down := NewProblem("err:form:invalid",
		WithSource("a-service"),
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}),
	)

	relayed := Relay(down, "an-edge", fasthttp.StatusBadRequest)

	qt.Check(t, qt.DeepEquals(relayed.Errors, FieldErrors{{Field: 15, Reason: "required"}}))

	relayed.Errors[0].Reason = "changed"
	qt.Check(t, qt.Equals(down.Errors[0].Reason, "required"))
}

func TestParseProblem_KeepsTheFieldList(t *testing.T) {
	orig := NewProblem("err:form:invalid",
		WithSource("a-service"),
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}, FieldError{Field: 16, Reason: "too_long"}),
	)
	body, err := json.Marshal(orig)
	qt.Assert(t, qt.IsNil(err))

	got, ok := ParseProblem(body)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.DeepEquals(got.Errors, orig.Errors))
	qt.Check(t, qt.IsFalse(got.fieldErrorsDropped))
	qt.Check(t, qt.Equals(got.Source, "a-service"))
}

func TestParseProblem_NoListIsNoListAndNotADrop(t *testing.T) {
	for name, errorsMember := range map[string]string{
		"absent":     ``,
		"null":       `,"errors":null`,
		"empty list": `,"errors":[]`,
	} {
		body := `{"title":"Invalid request","status":400,"code":"err:form:invalid"` + errorsMember + `}`

		got, ok := ParseProblem([]byte(body))
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf(name))
		qt.Check(t, qt.HasLen(got.Errors, 0), qt.Commentf(name))
		qt.Check(t, qt.IsFalse(got.fieldErrorsDropped), qt.Commentf(name))
	}
}

// TestParseProblem_TheLastOfTwoKeysWins pins that the member is read as the standard decoder
// reads a repeated or differently cased key, whichever way round: a bad list that comes last
// cannot hide behind a good one, and a good one that comes last is not lost to a bad one.
func TestParseProblem_TheLastOfTwoKeysWins(t *testing.T) {
	good := `[{"field":15,"reason":"required"}]`
	bad := `[{"field":0,"reason":"required"}]`
	head := `{"title":"Invalid request","status":400,"code":"err:form:invalid",`

	for name, tc := range map[string]struct {
		body string
		kept bool
	}{
		"bad last, same case":       {head + `"errors":` + good + `,"errors":` + bad + `}`, false},
		"good last, same case":      {head + `"errors":` + bad + `,"errors":` + good + `}`, true},
		"bad last, other case":      {head + `"errors":` + good + `,"Errors":` + bad + `}`, false},
		"good last, other case":     {head + `"ERRORS":` + bad + `,"errors":` + good + `}`, true},
		"good then null is no list": {head + `"errors":` + good + `,"errors":null}`, false},
	} {
		got, ok := ParseProblem([]byte(tc.body))
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf(name))

		if tc.kept {
			qt.Check(t, qt.DeepEquals(got.Errors, FieldErrors{{Field: 15, Reason: "required"}}), qt.Commentf(name))
		} else {
			qt.Check(t, qt.HasLen(got.Errors, 0), qt.Commentf(name))
		}
	}
}

func TestParseProblem_AcceptsTheLimits(t *testing.T) {
	longest := "a" + strings.Repeat("b", 31) // 32 characters
	fifty := make([]string, 0, MaxFieldErrors)
	for i := 1; i <= MaxFieldErrors; i++ {
		fifty = append(fifty, fmt.Sprintf(`{"field":%d,"reason":"required"}`, i))
	}

	for name, list := range map[string]string{
		"smallest field":    `[{"field":1,"reason":"x"}]`,
		"largest field":     `[{"field":2147483647,"reason":"required"}]`,
		"longest reason":    `[{"field":3,"reason":"` + longest + `"}]`,
		"digits and unders": `[{"field":3,"reason":"a_1_b2"}]`,
		"the most entries":  "[" + strings.Join(fifty, ",") + "]",
		"same field twice":  `[{"field":3,"reason":"required"},{"field":3,"reason":"too_long"}]`,
	} {
		body := `{"title":"Invalid request","status":400,"code":"err:form:invalid","errors":` + list + `}`

		got, ok := ParseProblem([]byte(body))
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf(name))
		qt.Check(t, qt.Not(qt.HasLen(got.Errors, 0)), qt.Commentf(name))
		qt.Check(t, qt.IsFalse(got.fieldErrorsDropped), qt.Commentf(name))
	}
}

func TestParseProblem_DropsAnInvalidListWholeAndKeepsTheRest(t *testing.T) {
	fiftyOne := make([]string, 0, MaxFieldErrors+1)
	for i := 1; i <= MaxFieldErrors+1; i++ {
		fiftyOne = append(fiftyOne, fmt.Sprintf(`{"field":%d,"reason":"required"}`, i))
	}

	// Every list holds one good entry beside the fault, so a partly trusted list would show.
	good := `{"field":15,"reason":"required"}`

	for name, list := range map[string]string{
		"fifty-one entries":         "[" + strings.Join(fiftyOne, ",") + "]",
		"field zero":                `[` + good + `,{"field":0,"reason":"required"}]`,
		"field negative":            `[` + good + `,{"field":-3,"reason":"required"}]`,
		"field above the range":     `[` + good + `,{"field":2147483648,"reason":"required"}]`,
		"field as text":             `[` + good + `,{"field":"16","reason":"required"}]`,
		"field fractional":          `[` + good + `,{"field":15.5,"reason":"required"}]`,
		"reason with a space":       `[` + good + `,{"field":16,"reason":"not ok"}]`,
		"reason in capitals":        `[` + good + `,{"field":16,"reason":"Required"}]`,
		"reason starting a digit":   `[` + good + `,{"field":16,"reason":"1st"}]`,
		"reason of 33 characters":   `[` + good + `,{"field":16,"reason":"a` + strings.Repeat("b", 32) + `"}]`,
		"reason empty":              `[` + good + `,{"field":16,"reason":""}]`,
		"reason with a line break":  `[` + good + `,{"field":16,"reason":"required\n"}]`,
		"reason with a sentence":    `[` + good + `,{"field":16,"reason":"the order number is empty"}]`,
		"an entry repeated":         `[` + good + `,` + good + `]`,
		"a name member":             `[` + good + `,{"field":16,"reason":"required","name":"order number"}]`,
		"a message member":          `[` + good + `,{"field":16,"reason":"required","message":"hello"}]`,
		"a null entry":              `[` + good + `,null]`,
		"an entry that is a number": `[` + good + `,16]`,
		"an object, not a list":     `{"field":15,"reason":"required"}`,
		"a string, not a list":      `"field 15 is required"`,
	} {
		body := `{"title":"Invalid request","status":400,"code":"err:form:invalid","detail":"kept","errors":` + list + `}`

		got, ok := ParseProblem([]byte(body))
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf(name))
		qt.Check(t, qt.HasLen(got.Errors, 0), qt.Commentf("%s: a bad list must go whole, not in part", name))
		qt.Check(t, qt.IsTrue(got.fieldErrorsDropped), qt.Commentf(name))
		// The rest of the problem still arrives.
		qt.Check(t, qt.Equals(got.Code, "err:form:invalid"), qt.Commentf(name))
		qt.Check(t, qt.Equals(got.Status, fasthttp.StatusBadRequest), qt.Commentf(name))
		qt.Check(t, qt.Equals(got.Title, "Invalid request"), qt.Commentf(name))
		qt.Check(t, qt.Equals(got.Detail, "kept"), qt.Commentf(name))
	}
}

// TestPublicProblem_AnEmbeddingTypeKeepsItsOwnFields is the same guard for the public
// projection: it has no JSON method of its own either.
func TestPublicProblem_AnEmbeddingTypeKeepsItsOwnFields(t *testing.T) {
	type wrapped struct {
		PublicProblem
		Extra string `json:"extra"`
	}

	body, err := json.Marshal(wrapped{
		PublicProblem: PublicProblem{Title: "t", Status: 400, Code: "err:x:y", Errors: FieldErrors{{Field: 15, Reason: "required"}}},
		Extra:         "kept",
	})
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(body), `"extra":"kept"`))
	qt.Check(t, qt.StringContains(string(body), `"code":"err:x:y"`))
	qt.Check(t, qt.StringContains(string(body), requiredFifteen))
}

// TestFieldErrors_ADirectDecodeNeverFailsAndChecksTheList: code that decodes a body into a
// Problem itself, not through ParseProblem, still gets the other members and never an error
// for a list shaped some other way; a list that breaks a rule is left out there too.
func TestFieldErrors_ADirectDecodeNeverFailsAndChecksTheList(t *testing.T) {
	head := `{"title":"Invalid request","status":400,"code":"err:form:invalid","errors":`

	for name, list := range map[string]string{
		"a map of messages":    `{"email":["is required"]}`,
		"a string":             `"field 15 is required"`,
		"a sentence as reason": `[{"field":1,"reason":"a sentence leaks"}]`,
		"an unknown member":    `[{"field":1,"reason":"required","name":"x"}]`,
	} {
		var p Problem

		qt.Check(t, qt.IsNil(json.Unmarshal([]byte(head+list+`}`), &p)), qt.Commentf(name))
		qt.Check(t, qt.Equals(p.Code, "err:form:invalid"), qt.Commentf(name))
		qt.Check(t, qt.HasLen(p.Errors, 0), qt.Commentf(name))
	}

	var kept Problem

	qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(head+`[{"field":15,"reason":"required"}]}`), &kept)))
	qt.Check(t, qt.DeepEquals(kept.Errors, FieldErrors{{Field: 15, Reason: "required"}}))
}

// TestProblem_ASharedProblemIsOnlyReadWhileRendering: a Problem held in a package variable
// and returned for many requests at once must not be written to by the paths that render
// it. Meaningful under the race detector; it also holds the list's value fixed. It covers
// toProblem, Public and MarshalError; the Handler itself is covered by
// TestHandler_ASharedProblemIsNotWrittenToAndKeepsNoRequestsTraceID.
func TestProblem_ASharedProblemIsOnlyReadWhileRendering(t *testing.T) {
	bad := NewProblem("err:form:invalid")
	bad.Errors = FieldErrors{{Field: 0, Reason: "required"}}

	good := NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 15, Reason: "required"}))

	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				for _, p := range []*Problem{bad, good} {
					_ = toProblem(p)
					_ = p.Public()
					_, _, _ = p.MarshalError("application/json")
					_ = !validFieldErrors(p.Errors)
				}
			}
		}()
	}

	wg.Wait()

	qt.Check(t, qt.HasLen(bad.Errors, 1))
	qt.Check(t, qt.HasLen(good.Errors, 1))
}

// TestProblem_AnEmbeddingTypeKeepsItsOwnFields pins that Problem has no JSON method of its
// own: one would be promoted onto every type that embeds a Problem and would silently
// stop that type's other exported fields from being written or read.
func TestProblem_AnEmbeddingTypeKeepsItsOwnFields(t *testing.T) {
	type wrapped struct {
		*Problem
		Extra string `json:"extra"`
	}

	body, err := json.Marshal(wrapped{
		Problem: NewProblem("err:form:invalid", WithFieldErrors(FieldError{Field: 15, Reason: "required"})),
		Extra:   "kept",
	})
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(body), `"extra":"kept"`))
	qt.Check(t, qt.StringContains(string(body), `"code":"err:form:invalid"`))
	qt.Check(t, qt.StringContains(string(body), requiredFifteen))

	got := wrapped{Problem: &Problem{}}
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &got)))
	qt.Check(t, qt.Equals(got.Extra, "kept"))
	qt.Check(t, qt.Equals(got.Code, "err:form:invalid"))
}

func TestWithFieldErrors_DropsAnInvalidListWhole(t *testing.T) {
	p := NewProblem("err:form:invalid",
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}, FieldError{Field: 0, Reason: "required"}),
	)

	qt.Check(t, qt.HasLen(p.Errors, 0))
	qt.Check(t, qt.IsTrue(p.fieldErrorsDropped))
	qt.Check(t, qt.Equals(p.Code, "err:form:invalid"))
}

// TestEmit_DropsAListSetByHandOnEveryPath: whoever writes a Problem, by whatever encoder
// (encoding/json by pointer or by value, MarshalError, the public projection, a
// PublicProblem built by hand), leaves out a list that is not valid, so none of it reaches
// the wire. The renderer and the web framework's own encoder are tested in the handler tests.
func TestEmit_DropsAListSetByHandOnEveryPath(t *testing.T) {
	bad := FieldErrors{{Field: 15, Reason: "required"}, {Field: 16, Reason: "Not A Word"}}

	p := NewProblem("err:form:invalid")
	p.Errors = bad

	byPointer, err := json.Marshal(p)
	qt.Assert(t, qt.IsNil(err))

	byValue, err := json.Marshal(*p)
	qt.Assert(t, qt.IsNil(err))

	viaMarshalError, _, ok := p.MarshalError("application/json")
	qt.Assert(t, qt.IsTrue(ok))

	public, err := json.Marshal(p.Public())
	qt.Assert(t, qt.IsNil(err))

	literal, err := json.Marshal(PublicProblem{Code: "err:form:invalid", Status: 400, Title: "t", Errors: bad})
	qt.Assert(t, qt.IsNil(err))

	for name, body := range map[string][]byte{
		"pointer": byPointer, "value": byValue, "MarshalError": viaMarshalError,
		"public projection": public, "public literal": literal,
	} {
		// Nothing of the list is written; at most an empty "errors":null stands where it was.
		qt.Check(t, qt.Not(qt.StringContains(string(body), `"field"`)), qt.Commentf("%s: %s", name, body))
		qt.Check(t, qt.Not(qt.StringContains(string(body), "required")), qt.Commentf("%s: %s", name, body))
		qt.Check(t, qt.Not(qt.StringContains(string(body), "Not A Word")), qt.Commentf("%s: %s", name, body))
		qt.Check(t, qt.StringContains(string(body), "err:form:invalid"), qt.Commentf(name))
	}

	// The projection built by Public leaves the member out altogether.
	qt.Check(t, qt.Not(qt.StringContains(string(public), `"errors"`)), qt.Commentf(string(public)))
}

func TestEmit_AValidListSetByHandIsWritten(t *testing.T) {
	p := NewProblem("err:form:invalid")
	p.Errors = []FieldError{{Field: 15, Reason: "required"}}

	viaMarshalError, _, ok := p.MarshalError("application/json")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.StringContains(string(viaMarshalError), requiredFifteen))

	public, err := json.Marshal(p.Public())
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(public), requiredFifteen))
}

// TestFieldError_CarriesNoFreeText pins the property the public boundary relies on:
// a field error is a number and a short word and nothing else. Adding a member to it
// reopens the leak the validation exists to prevent, so this fails until the change
// is argued for.
func TestFieldError_CarriesNoFreeText(t *testing.T) {
	typ := reflect.TypeOf(FieldError{})

	qt.Assert(t, qt.Equals(typ.NumField(), 2))
	qt.Check(t, qt.Equals(typ.Field(0).Name, "Field"))
	qt.Check(t, qt.Equals(typ.Field(0).Type.Kind(), reflect.Int))
	qt.Check(t, qt.Equals(typ.Field(1).Name, "Reason"))
	qt.Check(t, qt.Equals(typ.Field(1).Type.Kind(), reflect.String))

	body, err := json.Marshal(FieldError{Field: 15, Reason: "required"})
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(string(body), `{"field":15,"reason":"required"}`))
}

func TestFieldErrors_AnOlderReaderIgnoresTheMember(t *testing.T) {
	body, err := json.Marshal(NewProblem("err:form:invalid",
		WithFieldErrors(FieldError{Field: 15, Reason: "required"}),
	))
	qt.Assert(t, qt.IsNil(err))

	// A client written before the member existed knows only these members.
	var older struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
		Code   string `json:"code"`
	}

	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &older)))
	qt.Check(t, qt.Equals(older.Code, "err:form:invalid"))
	qt.Check(t, qt.Equals(older.Status, fasthttp.StatusBadRequest))
}
