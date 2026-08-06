package flat_test

import (
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/internal/testutil"
)

var errRejectedText = errors.New("rejected text")

type mutatingTextValue struct {
	Value string
}

func (v *mutatingTextValue) UnmarshalText(text []byte) error {
	v.Value = string(text)
	if v.Value == "invalid" {
		return errRejectedText
	}
	return nil
}

// endpointValue carries state that UnmarshalText never assigns: an exported
// pre-wired dependency and an unexported cache.
type endpointValue struct {
	Host   string
	Client *http.Client
	cached int
}

func (e *endpointValue) UnmarshalText(text []byte) error {
	e.Host = string(text)
	if e.Host == "invalid" {
		return errRejectedText
	}
	return nil
}

// hookedEndpoint carries a func field, which reflect.DeepEqual reports as
// unequal whenever it is not nil.
type hookedEndpoint struct {
	Host  string
	OnSet func(string)
}

func (e *hookedEndpoint) UnmarshalText(text []byte) error {
	e.Host = string(text)
	return nil
}

// valueReceiverText implements TextUnmarshaler on a value receiver, so it can
// never write the parsed value back into the field.
type valueReceiverText string

func (valueReceiverText) UnmarshalText([]byte) error { return nil }

func TestField(t *testing.T) {
	type nestedConfig struct {
		DSN string
	}

	type Config struct {
		First        string `default:"first" test:"test-tag"`
		Second       error
		NestedConfig nestedConfig
	}

	conf := Config{}
	fs, err := flat.View(&conf)
	if err != nil {
		t.Fatal(err)
	}

	if len(fs) != 3 {
		t.Fatalf("expected 3 fields but got %d", len(fs))
	}

	firstField := fs[0]

	if name := firstField.Name(); name != "First" {
		t.Errorf("expected First but got %v", name)
	}

	tag, ok := firstField.Tag("test")
	if !ok {
		t.Error("expected test tag on firstField but not found")
	}

	if tag != "test-tag" {
		t.Errorf("expected tag test to be test-tag but got %v", tag)
	}

	if !firstField.IsZero() {
		t.Error("expected IsZero() to return true")
	}

	meta1 := firstField.Meta()
	meta2 := firstField.Meta()

	meta1["test"] = "okay"

	testutil.Equal(t, meta1, meta2)

	if def := firstField.String(); def != "first" {
		t.Errorf("expected String() to return default tag value but got %v", def)
	}

	if err := firstField.Set("some-value"); err != nil {
		t.Errorf("expected Set() to return nil but got: %v", err)
	}

	if firstField.IsZero() {
		t.Error("expected IsZero() to return false")
	}

	secondField := fs[1]

	if !secondField.IsZero() {
		t.Error("expected IsZero() to return true")
	}

	conf.Second = errors.New("oh no")

	if secondField.IsZero() {
		t.Error("expected IsZero() to return false")
	}

	dsnField := fs[2]

	if name := dsnField.Name(); name != "NestedConfig.DSN" {
		t.Errorf("expected NestedConfig.DSN but got %v", name)
	}
}

func TestFieldSetDoesNotMutateOnParseError(t *testing.T) {
	t.Parallel()

	type Config struct {
		Bool  bool
		Int   int
		Uint  uint
		Float float64
		Slice []int
	}

	tests := []struct {
		name  string
		value string
	}{
		{name: "Bool", value: "not-bool"},
		{name: "Int", value: "not-int"},
		{name: "Uint", value: "-1"},
		{name: "Float", value: "not-float"},
		{name: "Slice", value: "4,not-int,6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := Config{Bool: true, Int: 1, Uint: 2, Float: 3, Slice: []int{1, 2, 3}}
			before := Config{Bool: config.Bool, Int: config.Int, Uint: config.Uint, Float: config.Float, Slice: append([]int(nil), config.Slice...)}
			fields, err := flat.View(&config)
			if err != nil {
				t.Fatalf("View() error = %v", err)
			}
			for _, field := range fields {
				if field.Name() != tt.name {
					continue
				}
				if err := field.Set(tt.value); err == nil {
					t.Fatalf("Set(%q) error = nil", tt.value)
				}
				if !reflect.DeepEqual(config, before) {
					t.Fatalf("Set(%q) mutated config: got %+v, want %+v", tt.value, config, before)
				}
				return
			}
			t.Fatalf("field %s not found", tt.name)
		})
	}
}

func TestFieldSetPreservesTextUnmarshalerPointerIdentity(t *testing.T) {
	t.Parallel()

	level := new(slog.LevelVar)
	level.Set(slog.LevelWarn)
	handler := slog.NewTextHandler(nil, &slog.HandlerOptions{Level: level})
	config := struct {
		Level *slog.LevelVar
	}{Level: level}

	fields, err := flat.View(&config)
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}
	if err := fields[0].Set("ERROR"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if config.Level != level {
		t.Fatal("Set() replaced the caller's TextUnmarshaler pointer")
	}
	if handler.Enabled(t.Context(), slog.LevelWarn) {
		t.Fatal("pre-wired handler still observes the old WARN level")
	}
	if !handler.Enabled(t.Context(), slog.LevelError) {
		t.Fatal("pre-wired handler does not observe the new ERROR level")
	}
}

func TestFieldSetTextUnmarshalerIsTransactional(t *testing.T) {
	t.Parallel()

	value := &mutatingTextValue{Value: "last-known-good"}
	config := struct {
		Value *mutatingTextValue
	}{Value: value}
	fields, err := flat.View(&config)
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}

	if err := fields[0].Set("invalid"); !errors.Is(err, errRejectedText) {
		t.Fatalf("Set(invalid) error = %v, want %v", err, errRejectedText)
	}
	if config.Value != value || value.Value != "last-known-good" {
		t.Fatalf("failed Set mutated value or identity: ptr=%p value=%q", config.Value, value.Value)
	}
	if err := fields[0].Set("new-value"); err != nil {
		t.Fatalf("Set(valid) error = %v", err)
	}
	if config.Value != value || value.Value != "new-value" {
		t.Fatalf("successful Set lost identity or value: ptr=%p value=%q", config.Value, value.Value)
	}
}

func TestFieldSetTextUnmarshalerKeepsUnassignedState(t *testing.T) {
	t.Parallel()

	endpoint := &endpointValue{Host: "old", Client: http.DefaultClient, cached: 42}
	config := struct {
		EP *endpointValue
	}{EP: endpoint}

	fields, err := flat.View(&config)
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}

	changed, err := fields[0].SetChanged("new")
	if err != nil {
		t.Fatalf("SetChanged() error = %v", err)
	}
	if !changed {
		t.Error("SetChanged() changed = false, want true")
	}
	if config.EP != endpoint {
		t.Errorf("SetChanged() replaced the caller's pointer: got %p, want %p", config.EP, endpoint)
	}
	if endpoint.Host != "new" {
		t.Errorf("Host = %q, want %q", endpoint.Host, "new")
	}
	if endpoint.Client != http.DefaultClient {
		t.Errorf("Client = %v, want the pre-wired http.DefaultClient", endpoint.Client)
	}
	if endpoint.cached != 42 {
		t.Errorf("cached = %d, want 42", endpoint.cached)
	}

	changed, err = fields[0].SetChanged("new")
	if err != nil {
		t.Fatalf("SetChanged() error = %v", err)
	}
	if changed {
		t.Error("SetChanged() with an unchanged value reported changed = true")
	}
}

func TestFieldSetTextUnmarshalerDoesNotMutateOnError(t *testing.T) {
	t.Parallel()

	endpoint := &endpointValue{Host: "last-known-good", Client: http.DefaultClient, cached: 42}
	before := *endpoint
	config := struct {
		EP *endpointValue
	}{EP: endpoint}

	fields, err := flat.View(&config)
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}

	changed, err := fields[0].SetChanged("invalid")
	if !errors.Is(err, errRejectedText) {
		t.Fatalf("SetChanged(invalid) error = %v, want %v", err, errRejectedText)
	}
	if changed {
		t.Error("SetChanged(invalid) changed = true, want false")
	}
	if config.EP != endpoint {
		t.Errorf("failed SetChanged replaced the caller's pointer: got %p, want %p", config.EP, endpoint)
	}
	if *endpoint != before {
		t.Errorf("failed SetChanged mutated the live value: got %+v, want %+v", *endpoint, before)
	}
}

func TestFieldSetTextUnmarshalerNilPointer(t *testing.T) {
	t.Parallel()

	t.Run("allocates", func(t *testing.T) {
		t.Parallel()

		config := struct {
			EP *endpointValue
		}{}
		fields, err := flat.View(&config)
		if err != nil {
			t.Fatalf("View() error = %v", err)
		}

		changed, err := fields[0].SetChanged("new")
		if err != nil {
			t.Fatalf("SetChanged() error = %v", err)
		}
		if !changed {
			t.Error("SetChanged() changed = false, want true")
		}
		if config.EP == nil {
			t.Fatal("SetChanged() left the nil pointer unallocated")
		}
		if config.EP.Host != "new" {
			t.Errorf("Host = %q, want %q", config.EP.Host, "new")
		}
	})

	t.Run("stays nil on error", func(t *testing.T) {
		t.Parallel()

		config := struct {
			EP *endpointValue
		}{}
		fields, err := flat.View(&config)
		if err != nil {
			t.Fatalf("View() error = %v", err)
		}

		if _, err := fields[0].SetChanged("invalid"); !errors.Is(err, errRejectedText) {
			t.Fatalf("SetChanged(invalid) error = %v, want %v", err, errRejectedText)
		}
		if config.EP != nil {
			t.Errorf("failed SetChanged allocated the field: got %+v, want nil", config.EP)
		}
	})
}

func TestFieldSetChangedIgnoresFuncState(t *testing.T) {
	t.Parallel()

	t.Run("func field", func(t *testing.T) {
		t.Parallel()

		config := struct {
			Hook func(string)
		}{Hook: func(string) {}}
		fields, err := flat.View(&config)
		if err != nil {
			t.Fatalf("View() error = %v", err)
		}

		changed, err := fields[0].SetChanged("anything")
		if err != nil {
			t.Fatalf("SetChanged() error = %v", err)
		}
		if changed {
			t.Error("SetChanged() on an untouched func field reported changed = true")
		}
	})

	t.Run("func inside TextUnmarshaler", func(t *testing.T) {
		t.Parallel()

		endpoint := &hookedEndpoint{Host: "same", OnSet: func(string) {}}
		config := struct {
			EP *hookedEndpoint
		}{EP: endpoint}
		fields, err := flat.View(&config)
		if err != nil {
			t.Fatalf("View() error = %v", err)
		}

		changed, err := fields[0].SetChanged("same")
		if err != nil {
			t.Fatalf("SetChanged() error = %v", err)
		}
		if changed {
			t.Error("SetChanged() with an unchanged value reported changed = true because of the func field")
		}

		changed, err = fields[0].SetChanged("other")
		if err != nil {
			t.Fatalf("SetChanged() error = %v", err)
		}
		if !changed {
			t.Error("SetChanged() with a new value reported changed = false")
		}
		if endpoint.Host != "other" {
			t.Errorf("Host = %q, want %q", endpoint.Host, "other")
		}
		if endpoint.OnSet == nil {
			t.Error("SetChanged() wiped the func field")
		}
	})
}

func TestFieldSetValueReceiverTextUnmarshaler(t *testing.T) {
	t.Parallel()

	// A value receiver gets a copy, so it can never apply the parsed value.
	// flat.View routes such types here for every non-struct field shape, and the
	// field must be reported instead of silently zeroed.
	t.Run("scalar field", func(t *testing.T) {
		t.Parallel()

		config := struct {
			Mode valueReceiverText
		}{Mode: "old"}
		fields, err := flat.View(&config)
		if err != nil {
			t.Fatalf("View() error = %v", err)
		}

		changed, err := fields[0].SetChanged("new")
		if err == nil {
			t.Fatal("SetChanged() error = nil, want a value-receiver error")
		}
		for _, want := range []string{"Mode", "valueReceiverText", "pointer receiver"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("SetChanged() error = %q, want it to mention %q", err, want)
			}
		}
		if changed {
			t.Error("SetChanged() changed = true, want false")
		}
		if config.Mode != "old" {
			t.Errorf("Mode = %q, want %q: the field must not be zeroed", config.Mode, "old")
		}
	})

	t.Run("map entry", func(t *testing.T) {
		t.Parallel()

		config := struct {
			Modes map[string]valueReceiverText
		}{Modes: map[string]valueReceiverText{"a": "old"}}
		fields, err := flat.View(&config)
		if err != nil {
			t.Fatalf("View() error = %v", err)
		}
		if len(fields) != 1 {
			t.Fatalf("fields = %d, want 1", len(fields))
		}

		if _, err := fields[0].SetChanged("new"); err == nil {
			t.Fatal("SetChanged() error = nil, want a value-receiver error")
		}
		if config.Modes["a"] != "old" {
			t.Errorf("Modes[a] = %q, want %q: the entry must not be zeroed", config.Modes["a"], "old")
		}
	})
}
