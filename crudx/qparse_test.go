package crudx

import (
	"errors"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

func testSpec() ListSpec {
	return ListSpec{
		Fields: map[string]FieldSpec{
			"name":    {Column: "users.name", Kind: FieldString, Indexed: true},
			"state":   {Column: "users.state", Kind: FieldString},
			"cpuNum":  {Column: "users.cpu_num", Kind: FieldNumber},
			"memory":  {Column: "users.memory", Kind: FieldNumber},
			"enable":  {Column: "users.enable", Kind: FieldBool},
			"description": {Column: "users.description", Kind: FieldString},
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
	_, err := ParseQConditions(spec, []string{"state=Running", "cpuNum>=4", "name~x"})
	assertInvalid(t, err, ReasonTooManyConditions)
}

func TestParseLiteral_types(t *testing.T) {
	v, err := parseLiteral("true")
	if err != nil || v != true {
		t.Fatalf("true: %v %v", v, err)
	}
	v, err = parseLiteral("100")
	if err != nil || v != int64(100) {
		t.Fatalf("100: %v", v)
	}
	v, err = parseLiteral("100vm")
	if err != nil || v != "100vm" {
		t.Fatalf("100vm: %v", v)
	}
	v, err = parseLiteral("null")
	if err != nil || v != nil {
		t.Fatalf("null: %v", v)
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

func TestParseHTTPQValues_decodeFailed(t *testing.T) {
	_, err := ParseHTTPQValues([]string{"%ZZ"})
	assertInvalid(t, err, ReasonURLDecodeFailed)
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
