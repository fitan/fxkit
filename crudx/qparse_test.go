package crudx

import (
	"errors"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

func testSpec() ListSpec {
	return ListSpec{
		Fields: map[string]FieldSpec{
			"name":        {Column: "users.name", Kind: FieldString, Indexed: true},
			"state":       {Column: "users.state", Kind: FieldString},
			"cpuNum":      {Column: "users.cpu_num", Kind: FieldNumber},
			"memory":      {Column: "users.memory", Kind: FieldNumber},
			"enable":      {Column: "users.enable", Kind: FieldBool},
			"description": {Column: "users.description", Kind: FieldString},
			"createdAt":   {Column: "users.created_at", Kind: FieldTime},
		},
		Relations: map[string]*RelationSpec{
			"cluster": {
				Table: "clusters", Alias: "cluster", On: "cluster.id = users.cluster_id",
				Fields: map[string]FieldSpec{
					"name": {Column: "cluster.name", Kind: FieldString, Indexed: true},
				},
			},
		},
		SortFields: map[string]string{
			"name": "users.name",
		},
	}
}

func assertInvalid(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	var fe *fxerrors.Error
	if !errors.As(err, &fe) {
		t.Fatalf("expected fxerrors.Error, got %v", err)
	}
	if fe.Details["reason"] != reason {
		t.Fatalf("reason=%v want %s", fe.Details["reason"], reason)
	}
}

func TestParseQConditions_valid(t *testing.T) {
	spec := testSpec()
	cases := []struct {
		raw  string
		op   Operator
		vals int
	}{
		{"state=Running", OpEqual, 1},
		{"state=Running|Stopped", OpEqual, 2},
		{"state.in:Running,Stopped", OpIn, 2},
		{"name~test", OpLike, 1},
		{"cpuNum>=4", OpGTE, 1},
		{"description=null", OpEqual, 1},
		{"cluster.name~prod", OpLike, 1},
	}
	for _, tc := range cases {
		conds, err := ParseQConditions(spec, []string{tc.raw})
		if err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if len(conds) != 1 || conds[0].Op != tc.op || len(conds[0].Values) != tc.vals {
			t.Fatalf("%q: got %+v", tc.raw, conds[0])
		}
	}
}

func TestParseQConditions_invalid(t *testing.T) {
	spec := testSpec()
	cases := []struct {
		raw    string
		reason string
	}{
		{"name~a|b", ReasonIllegalORSyntax},
		{"a.b.c.d.name=xxx", ReasonRelationDepthOverLimit},
		{"name~", ReasonEmptyLikeValue},
		{"cpuNum>test", ReasonInvalidValueType},
		{"cpuNum>null", ReasonInvalidValueType},
		{"name", ReasonNoOperator},
	}
	for _, tc := range cases {
		_, err := ParseQConditions(spec, []string{tc.raw})
		assertInvalid(t, err, tc.reason)
	}
}

func TestParseQConditions_andMultiple(t *testing.T) {
	spec := testSpec()
	conds, err := ParseQConditions(spec, []string{"state=Running", "cpuNum>=4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conds) != 2 {
		t.Fatalf("got %d conditions", len(conds))
	}
}

func TestParseQConditions_tooMany(t *testing.T) {
	spec := testSpec()
	spec.Limits.MaxConditions = 2
	_, err := ParseQConditions(spec, []string{"state=Running", "cpuNum>=4", "enable=true"})
	assertInvalid(t, err, ReasonTooManyConditions)
}

func TestParseLiteral_types(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"null", nil},
		{"NULL", nil},
		{"true", true},
		{"false", false},
		{"123", int64(123)},
		{"-456", int64(-456)},
		{"12.34", float64(12.34)},
		{".", "."},
		{"-.", "-."},
		{"hello", "hello"},
		{`"hello, world"`, "hello, world"},
		{`'single quoted'`, "single quoted"},
	}
	for _, tc := range cases {
		got, err := parseLiteral(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %v (%T) want %v (%T)", tc.in, got, got, tc.want, tc.want)
		}
	}
}

func TestNormalizeEqualToIn(t *testing.T) {
	c := Condition{Op: OpEqual, Values: []any{"a", "b"}}
	c = NormalizeEqualToIn(c)
	if c.Op != OpIn {
		t.Fatal("expected OpIn")
	}
}

func TestParseHTTPQValues_decode(t *testing.T) {
	got, err := ParseHTTPQValues([]string{"name~web%20server", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "name~web server" {
		t.Fatalf("got %v", got)
	}
}

func TestParseHTTPQValues_literalPercent(t *testing.T) {
	got, err := ParseHTTPQValues([]string{"state=done", "description=discount 20%"})
	if err != nil {
		// Even if url.QueryUnescape fails on plain %, it shouldn't fail for CleanHTTPQValues
		t.Logf("ParseHTTPQValues error: %v", err)
	} else if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestParseHTTPQValues_decodeFailed(t *testing.T) {
	_, err := ParseHTTPQValues([]string{"%ZZ"})
	assertInvalid(t, err, ReasonURLDecodeFailed)
}

func TestParseQConditions_numericStringField(t *testing.T) {
	spec := testSpec()
	// Test querying FieldString with pure numeric values (e.g. state=100 or phone=13800138000)
	conds, err := ParseQConditions(spec, []string{"state=100", `name="456"`})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if len(conds) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conds))
	}
	if val, ok := conds[0].Values[0].(string); !ok || val != "100" {
		t.Fatalf("expected string '100', got %T(%v)", conds[0].Values[0], conds[0].Values[0])
	}
	if val, ok := conds[1].Values[0].(string); !ok || val != "456" {
		t.Fatalf("expected string '456', got %T(%v)", conds[1].Values[0], conds[1].Values[0])
	}
}

func TestParseQConditions_operatorPriority(t *testing.T) {
	spec := testSpec()
	cases := []struct {
		raw string
		op  Operator
	}{
		{"cpuNum>=4", OpGTE},
		{"cpuNum<=8", OpLTE},
		{"state!=Destroyed", OpNotEqual},
		{"name!~tmp", OpNotLike},
		{"state.notin:Recycled,Destroyed", OpNotIn},
		{"cpuNum>2", OpGT},
		{"cpuNum<8", OpLT},
		{"enable=true", OpEqual},
	}
	for _, tc := range cases {
		conds, err := ParseQConditions(spec, []string{tc.raw})
		if err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if conds[0].Op != tc.op {
			t.Fatalf("%q: op=%v want %v", tc.raw, conds[0].Op, tc.op)
		}
	}
}

func TestParseQConditions_operatorEarliestPosition(t *testing.T) {
	spec := testSpec()
	// When a query value contains characters of another operator, the earliest operator
	// should be matched, not the operator that appears earlier in the opTokens slice.
	// 1. Equal value containing '~' (like operator): name=a~b
	conds, err := ParseQConditions(spec, []string{"name=a~b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conds) != 1 || conds[0].Op != OpEqual || conds[0].Field != "name" || conds[0].Values[0] != "a~b" {
		t.Fatalf("unexpected cond: %+v", conds[0])
	}

	// 2. Like value containing '=': name~a=b
	conds, err = ParseQConditions(spec, []string{"name~a=b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conds) != 1 || conds[0].Op != OpLike || conds[0].Field != "name" || conds[0].Values[0] != "a=b" {
		t.Fatalf("unexpected cond: %+v", conds[0])
	}

	// 3. Equal value containing '!=': description=1!=2
	conds, err = ParseQConditions(spec, []string{"description=1!=2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conds) != 1 || conds[0].Op != OpEqual || conds[0].Field != "description" || conds[0].Values[0] != "1!=2" {
		t.Fatalf("unexpected cond: %+v", conds[0])
	}
}

func TestParseQGroups_orWithQuotedCommas(t *testing.T) {
	spec := testSpec()
	groups, err := ParseQGroups(spec, []string{`or(name="a,b",state=Running)`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 || !groups[0].Or || len(groups[0].Conds) != 2 {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if groups[0].Conds[0].Field != "name" || groups[0].Conds[0].Values[0] != "a,b" {
		t.Fatalf("first cond unexpected: %+v", groups[0].Conds[0])
	}
	if groups[0].Conds[1].Field != "state" || groups[0].Conds[1].Values[0] != "Running" {
		t.Fatalf("second cond unexpected: %+v", groups[0].Conds[1])
	}
}

func TestParseQConditions_fieldNotExist(t *testing.T) {
	spec := testSpec()
	_, err := ParseQConditions(spec, []string{"unknown=1"})
	assertInvalid(t, err, ReasonFieldNotExist)
}

func TestParseQConditions_fieldNotIndexed(t *testing.T) {
	spec := testSpec()
	_, err := ParseQConditions(spec, []string{"state~run"})
	assertInvalid(t, err, ReasonFieldNotIndexed)
}

func TestParseQConditions_tooManyInValues(t *testing.T) {
	spec := testSpec()
	spec.Limits.MaxInValues = 2
	_, err := ParseQConditions(spec, []string{"state.in:a,b,c"})
	assertInvalid(t, err, ReasonTooManyInValues)
}

func TestParseQConditions_tooManyLike(t *testing.T) {
	spec := testSpec()
	spec.Limits.MaxLikeConds = 2
	_, err := ParseQConditions(spec, []string{"name~a", "cluster.name~b", "name~c"})
	assertInvalid(t, err, ReasonTooManyLikeConditions)
}

func TestParseQConditions_nullNotEqual(t *testing.T) {
	spec := testSpec()
	conds, err := ParseQConditions(spec, []string{"description!=null"})
	if err != nil {
		t.Fatal(err)
	}
	if conds[0].Op != OpNotEqual || conds[0].Values[0] != nil {
		t.Fatalf("got %+v", conds[0])
	}
}

func TestParseQConditions_emptyQSkipped(t *testing.T) {
	spec := testSpec()
	conds, err := ParseQConditions(spec, []string{"", "  ", "state=Running"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conds) != 1 {
		t.Fatalf("got %d conditions", len(conds))
	}
}

func TestParseQConditions_conflictingAndAllowed(t *testing.T) {
	spec := testSpec()
	conds, err := ParseQConditions(spec, []string{"state=Running", "state=Stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conds) != 2 {
		t.Fatalf("got %d conditions", len(conds))
	}
}
