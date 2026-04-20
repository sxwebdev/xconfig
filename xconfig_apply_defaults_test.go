package xconfig_test

import (
	"strings"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/internal/testutil"
)

type applyItem struct {
	Enabled bool   `default:"true"`
	Name    string `default:"anon"`
	Port    int    `default:"8080"`
}

type applyRoot struct {
	Address string      `default:"https://example.com"`
	Nested  applyItem   // nested struct with its own defaults
	Items   []applyItem // slice of struct — the bug this helper fixes
}

func TestApplyDefaults_Struct(t *testing.T) {
	v := applyRoot{
		Items: []applyItem{
			{Name: "first"},
			{Enabled: true, Name: "explicit", Port: 1234},
		},
	}

	if err := xconfig.ApplyDefaults(&v); err != nil {
		t.Fatal(err)
	}

	want := applyRoot{
		Address: "https://example.com",
		Nested:  applyItem{Enabled: true, Name: "anon", Port: 8080},
		Items: []applyItem{
			{Enabled: true, Name: "first", Port: 8080},
			{Enabled: true, Name: "explicit", Port: 1234},
		},
	}

	testutil.Equal(t, want, v)
}

func TestApplyDefaults_SliceOfStructs(t *testing.T) {
	groups := []applyItem{
		{Name: "a"},
		{Name: "b"},
	}

	if err := xconfig.ApplyDefaults(&groups); err != nil {
		t.Fatal(err)
	}

	want := []applyItem{
		{Enabled: true, Name: "a", Port: 8080},
		{Enabled: true, Name: "b", Port: 8080},
	}

	testutil.Equal(t, want, groups)
}

func TestApplyDefaults_SliceOfPointers(t *testing.T) {
	groups := []*applyItem{
		{Name: "a"},
		nil,
		{Enabled: true, Name: "explicit", Port: 1234},
	}

	if err := xconfig.ApplyDefaults(&groups); err != nil {
		t.Fatal(err)
	}

	want := []*applyItem{
		{Enabled: true, Name: "a", Port: 8080},
		nil,
		{Enabled: true, Name: "explicit", Port: 1234},
	}

	testutil.Equal(t, want, groups)
}

func TestApplyDefaults_RejectsNonPointer(t *testing.T) {
	v := applyItem{}
	err := xconfig.ApplyDefaults(v)
	if err == nil {
		t.Fatal("expected error for non-pointer argument")
	}
	if !strings.Contains(err.Error(), "pointer") {
		t.Fatalf("expected 'pointer' in error, got: %v", err)
	}
}

func TestApplyDefaults_RejectsNil(t *testing.T) {
	if err := xconfig.ApplyDefaults(nil); err == nil {
		t.Fatal("expected error for nil argument")
	}

	var p *applyItem
	if err := xconfig.ApplyDefaults(p); err == nil {
		t.Fatal("expected error for typed nil pointer")
	}
}

func TestApplyDefaults_PrimitiveSliceIsNoop(t *testing.T) {
	v := []string{"a", "b"}
	if err := xconfig.ApplyDefaults(&v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.Equal(t, []string{"a", "b"}, v)
}

func TestApplyDefaults_PreservesExplicitZeroFalseOnlyForPointer(t *testing.T) {
	// For a plain bool field, Go cannot distinguish an explicit "false" from
	// "not set" — the default wins. Callers who need the distinction must use
	// *bool. Both behaviours are asserted here so we notice if either changes.
	type withBool struct {
		Enabled bool  `default:"true"`
		PtrBool *bool `default:"true"`
	}

	explicitFalse := false
	v := withBool{PtrBool: &explicitFalse}

	if err := xconfig.ApplyDefaults(&v); err != nil {
		t.Fatal(err)
	}

	if !v.Enabled {
		t.Error("plain bool: default should have overwritten zero value")
	}
	if v.PtrBool == nil || *v.PtrBool != false {
		t.Errorf("pointer bool: explicit false must be preserved, got %v", v.PtrBool)
	}
}
