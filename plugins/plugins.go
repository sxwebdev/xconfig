// Package plugins describes the xconfig provider interface.
// it exists to enable xconfig.Classic without circular deps.
package plugins

import (
	"context"
	"errors"
	"log"
	"runtime"

	"github.com/sxwebdev/xconfig/flat"
)

// Plugin is the common interface for all xconfig providers.
type Plugin interface {
	Parse() error
}

// Walker is the interface for providers that take the whole
// config, like file loaders.
type Walker interface {
	Plugin

	Walk(config any) error
}

// Visitor is the interface for providers that require a flat view
// of the config, like flags, env vars.
type Visitor interface {
	Plugin

	Visit(fields flat.Fields) error
}

var tags = map[string]string{}

// ErrUsage is returned when user has request usage message
// via some plugin, mostly flags.
var ErrUsage = errors.New("xconfig: usage request")

// RegisterTag allows providers to ensure their tag is unique.
// they must call this function from an init.
func RegisterTag(name string) {
	if pkg, exists := tags[name]; exists {
		log.Panicf("tag %s already registered by %s", name, pkg)
	}

	pc, _, _, _ := runtime.Caller(1) //nolint:dogsled
	tags[name] = runtime.FuncForPC(pc).Name()
}

// FieldChange describes a single config field change detected during refresh.
type FieldChange struct {
	// FieldName is the full flat field path, e.g. "Database.Postgres.Password".
	FieldName string
}

// RefreshOutcome contains the non-fatal result of refreshing one plugin.
// Warnings describe individual values that were rejected while other valid
// values were applied. Warning errors must never contain secret values.
type RefreshOutcome struct {
	Changes  []FieldChange
	Warnings []error
}

// Refreshable is implemented by plugins that support background config refresh.
// Examples: vault, consul, etcd, AWS SSM sourcers.
type Refreshable interface {
	Plugin
	// Refresh re-fetches values and applies them to target. target is a private
	// working copy owned by xconfig and is discarded when Refresh returns an
	// error. Changes must describe mutations made during this call. Plugins
	// should compare against target instead of advancing a private baseline,
	// because failed cycles are discarded without a commit callback.
	Refresh(ctx context.Context, target any) (RefreshOutcome, error)
}
