package main

import "testing"

func TestParseResource_RejectsTableNameField(t *testing.T) {
	if _, err := parseResourceArgs("Thing", "", []string{"table_name:string"}, nil, nil, ""); err == nil {
		t.Fatal("table_name must be rejected: it collides with the generated TableName() method")
	}
}

func TestValidateModulePath(t *testing.T) {
	for _, ok := range []string{"example.com/app", "github.com/me/repo", "gopkg.in/yaml.v3"} {
		if err := validateModulePath(ok); err != nil {
			t.Errorf("validateModulePath(%q)=%v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "example.com/app\nrequire evil/x v1.0.0", "example.com//app", "/app", "app/", "a b"} {
		if err := validateModulePath(bad); err == nil {
			t.Errorf("validateModulePath(%q)=nil, want error", bad)
		}
	}
}

func TestComposeProjectName(t *testing.T) {
	cases := map[string]string{
		"MyApp":       "myapp",
		"a:b":         "a-b",
		"  Foo.Bar  ": "foo-bar",
		"1app":        "1app",
	}
	for in, want := range cases {
		if got := composeProjectName(in); got != want {
			t.Errorf("composeProjectName(%q)=%q, want %q", in, got, want)
		}
	}
}
