// Package xconfig provides advanced command line flags supporting defaults, env vars, and config structs.
package xconfig

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

const defaultTag = "default"

const maxQueuedWarnings = 16

var ErrUsage = plugins.ErrUsage

// ErrInvalidRefreshInterval is returned when StartRefresh receives a zero or
// negative interval. The currently running loop, if any, is left unchanged.
var ErrInvalidRefreshInterval = errors.New("xconfig: refresh interval must be positive")

// ErrNoRefreshablePlugins is returned when StartRefresh is called on a
// configuration without any plugin implementing plugins.Refreshable, because
// such a loop could never report anything. The currently running loop, if any,
// is left unchanged.
var ErrNoRefreshablePlugins = errors.New("xconfig: no plugin supports refresh")

// Config is the config manager.
type Config interface {
	// Parse will call the parse method of all the added pluginss in the order
	// that the pluginss were registered, it will return early as soon as any
	// plugins fails.
	// You must call this before using the config value.
	Parse() error

	// Usage provides a simple usage message based on the meta data registered
	// by the pluginss.
	Usage() (string, error)

	// Snapshot copies the latest successfully parsed or refreshed configuration
	// into dst. dst must be a non-nil pointer to the same type passed to Custom
	// or Load. The configuration is captured when Parse succeeds and is replaced
	// by every successful refresh, so later direct writes to the struct passed to
	// Custom or Load are never observed and every caller sees the same value
	// regardless of when it first calls Snapshot.
	//
	// The copy owns mutable config containers, including map keys and values
	// such as *big.Int or *bytes.Buffer, and can be read or mutated without
	// racing with background refresh. Because keys are copied as well, a key
	// containing pointers is a distinct key in the copy.
	//
	// Two kinds of value keep their original identity instead: fields tagged
	// with xconfig_shared:"true", which therefore must be safe for concurrent
	// use, and the immutable stdlib pointers *time.Location and *regexp.Regexp.
	// Inside a struct without exported fields (time.Time, netip.Addr,
	// unique.Handle, ...) pointers are shared rather than cloned, because those
	// are the interned or sentinel payloads such a type compares against; its
	// slices and maps are still cloned.
	Snapshot(dst any) error

	// UnknownFields returns fields found by file loaders but not represented in
	// the configuration type.
	UnknownFields() map[string][]string

	// Refresh synchronously refreshes every plugin implementing
	// plugins.Refreshable and atomically publishes the resulting snapshot. If a
	// plugin fails, the last successfully published snapshot remains current.
	Refresh(ctx context.Context) RefreshResult

	// StartRefresh starts a background goroutine that periodically calls Refresh.
	// The bounded returned channel reports changes, warnings, and errors and is
	// closed when the loop stops. Slow or absent consumers never block refresh;
	// coalesced events are reported through RefreshResult.Dropped. Starting a
	// valid new loop replaces the previous one. A non-positive interval or a
	// configuration without any plugins.Refreshable plugin is rejected with
	// ErrInvalidRefreshInterval or ErrNoRefreshablePlugins, leaving a running
	// loop unchanged.
	StartRefresh(ctx context.Context, interval time.Duration) (<-chan RefreshResult, error)

	// StopRefresh stops the background refresh goroutine and waits for it to finish.
	StopRefresh()
}

// RefreshResult describes one synchronous refresh or an event emitted by
// Config.StartRefresh. Changes and warnings never contain secret values.
type RefreshResult struct {
	Changes   []plugins.FieldChange
	Warnings  []error
	Err       error
	Published bool
	Dropped   uint64
}

// Custom returns a new Config. The conf must be a pointer to a struct.
func Custom(conf any, ps ...plugins.Plugin) (Config, error) {
	return newConfig(conf, ps...)
}

func newConfig(conf any, ps ...plugins.Plugin) (*config, error) {
	c := &config{
		target:  conf,
		plugins: make([]plugins.Plugin, 0, len(ps)),
	}

	fields, err := flat.View(conf)
	if err != nil {
		return c, err
	}
	c.fields = fields

	for _, plug := range ps {
		err := c.addPlugin(plug)
		if err != nil {
			return c, err
		}
	}

	return c, nil
}

type config struct {
	plugins []plugins.Plugin
	target  any // caller-owned value used only by Parse
	staging any // private mutable value passed to Refreshable plugins
	fields  flat.Fields
	options *options

	operationMu sync.Mutex
	usageMu     sync.Mutex
	dataMu      sync.RWMutex
	current     any
	parsed      bool
	refreshable bool

	refreshMu     sync.Mutex
	refreshCancel context.CancelFunc
	refreshDone   chan struct{}
}

func (c *config) addPlugin(plug plugins.Plugin) error { //nolint:funcorder
	var atOnceChecked bool

	// if the plugin is a Walker, we need to call Walk on it.
	walkerPlugin, ok := plug.(plugins.Walker)
	if ok {
		err := walkerPlugin.Walk(c.target)
		if err != nil {
			return err
		}
		atOnceChecked = true
	}

	// if the plugin is a Visitor, we need to call Visit on it.
	visitorPlugin, ok := plug.(plugins.Visitor)
	if ok {
		err := visitorPlugin.Visit(c.fields)
		if err != nil {
			return err
		}
		atOnceChecked = true
	}

	// if the plugin is neither, we return an error.
	if !atOnceChecked {
		return errors.New("unsupported plugins. expecting a Walker or Visitor")
	}

	c.plugins = append(c.plugins, plug)
	if _, ok := plug.(plugins.Refreshable); ok {
		c.refreshable = true
	}
	return nil
}

func (c *config) Parse() error {
	return c.parse(true)
}

func (c *config) parse(publishSnapshot bool) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()

	for _, p := range c.plugins {
		err := p.Parse()
		if err != nil {
			return err
		}
	}
	if !publishSnapshot {
		return nil
	}
	c.staging = nil
	current, err := cloneConfigPointer(c.target)
	if err != nil {
		return fmt.Errorf("snapshot parsed configuration: %w", err)
	}
	c.parsed = true
	c.publish(current)
	return nil
}

func (c *config) Refresh(ctx context.Context) RefreshResult {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()

	result := RefreshResult{}
	if !c.parsed {
		result.Err = ErrNotParsed
		return result
	}
	if !c.refreshable {
		return result
	}
	current := c.currentSnapshot()
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}

	if c.staging == nil {
		staging, err := cloneConfigPointer(current)
		if err != nil {
			result.Err = fmt.Errorf("prepare refresh staging: %w", err)
			return result
		}
		c.staging = staging
	}

	changedFields := make(map[string]struct{})
	for _, p := range c.plugins {
		refreshable, ok := p.(plugins.Refreshable)
		if !ok {
			continue
		}
		outcome, err := refreshable.Refresh(ctx, c.staging)
		result.Warnings = append(result.Warnings, outcome.Warnings...)
		if err != nil {
			result.Err = fmt.Errorf("refresh %T: %w", p, err)
			c.staging = nil
			return result
		}
		for _, change := range outcome.Changes {
			changedFields[change.FieldName] = struct{}{}
		}
	}

	// Plugins report value changes only, so container expansion that adds
	// zero-valued map entries or slice elements is detected here instead.
	if len(changedFields) == 0 && sameConfigData(c.staging, current) {
		return result
	}
	current, err := cloneConfigPointer(c.staging)
	if err != nil {
		result.Err = fmt.Errorf("publish refreshed configuration: %w", err)
		c.staging = nil
		return result
	}

	result.Changes = make([]plugins.FieldChange, 0, len(changedFields))
	for fieldName := range changedFields {
		result.Changes = append(result.Changes, plugins.FieldChange{FieldName: fieldName})
	}
	sortFieldChanges(result.Changes)
	c.publish(current)
	result.Published = true
	return result
}

func (c *config) StartRefresh(ctx context.Context, interval time.Duration) (<-chan RefreshResult, error) {
	if interval <= 0 {
		return nil, ErrInvalidRefreshInterval
	}
	if !c.refreshable {
		return nil, ErrNoRefreshablePlugins
	}
	results := make(chan RefreshResult, 16)

	c.refreshMu.Lock()
	if c.refreshCancel != nil {
		c.refreshCancel()
		<-c.refreshDone
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.refreshCancel = cancel
	c.refreshDone = done
	c.refreshMu.Unlock()

	go func() {
		defer close(done)
		defer close(results)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				result := c.Refresh(refreshCtx)
				if refreshCtx.Err() != nil {
					return
				}
				if result.Err == nil && !result.Published && len(result.Warnings) == 0 {
					continue
				}
				deliverRefreshResult(results, result)
			}
		}
	}()

	return results, nil
}

func (c *config) currentSnapshot() any {
	c.dataMu.RLock()
	defer c.dataMu.RUnlock()
	return c.current
}

func (c *config) publish(current any) {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	c.current = current
}

func deliverRefreshResult(results chan RefreshResult, result RefreshResult) {
	result.Warnings = limitWarnings(result.Warnings)
	select {
	case results <- result:
		return
	default:
	}

	select {
	case previous := <-results:
		result.Dropped += previous.Dropped + 1
		result.Published = result.Published || previous.Published
		result.Changes = mergeFieldChanges(previous.Changes, result.Changes)
		result.Err = mergeRefreshErrors(previous.Err, result.Err)
		result.Warnings = limitWarnings(slices.Concat(previous.Warnings, result.Warnings))
	default:
	}

	results <- result
}

type coalescedRefreshError struct {
	first  error
	latest error
}

func (e *coalescedRefreshError) Error() string {
	return fmt.Sprintf("%v; latest refresh error: %v", e.first, e.latest)
}

func (e *coalescedRefreshError) Unwrap() []error {
	return []error{e.first, e.latest}
}

func mergeRefreshErrors(first, latest error) error {
	if first == nil {
		return latest
	}
	if latest == nil {
		return first
	}
	if coalesced, ok := first.(*coalescedRefreshError); ok {
		first = coalesced.first
	}
	if coalesced, ok := latest.(*coalescedRefreshError); ok {
		latest = coalesced.latest
	}
	return &coalescedRefreshError{first: first, latest: latest}
}

func limitWarnings(warnings []error) []error {
	if len(warnings) <= maxQueuedWarnings {
		return slices.Clone(warnings)
	}
	half := maxQueuedWarnings / 2
	limited := make([]error, 0, maxQueuedWarnings)
	limited = append(limited, warnings[:half]...)
	limited = append(limited, warnings[len(warnings)-(maxQueuedWarnings-half):]...)
	return limited
}

func mergeFieldChanges(first, second []plugins.FieldChange) []plugins.FieldChange {
	fields := make(map[string]plugins.FieldChange, len(first)+len(second))
	for _, change := range first {
		fields[change.FieldName] = change
	}
	for _, change := range second {
		fields[change.FieldName] = change
	}
	merged := make([]plugins.FieldChange, 0, len(fields))
	for _, change := range fields {
		merged = append(merged, change)
	}
	sortFieldChanges(merged)
	return merged
}

func sortFieldChanges(changes []plugins.FieldChange) {
	slices.SortFunc(changes, func(a, b plugins.FieldChange) int {
		return cmp.Compare(a.FieldName, b.FieldName)
	})
}

func (c *config) StopRefresh() {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if c.refreshCancel != nil {
		c.refreshCancel()
		<-c.refreshDone
		c.refreshCancel = nil
		c.refreshDone = nil
	}

	c.operationMu.Lock()
	c.staging = nil
	c.operationMu.Unlock()
}

// ApplyDefaults applies `default:` struct tags to v. Non-zero fields are left
// intact, so existing values (including those loaded from a YAML/JSON file via
// an external unmarshaler) are preserved.
//
// v must be a non-nil pointer to one of:
//   - a struct;
//   - a slice of structs ([]T);
//   - a slice of pointers to structs ([]*T).
//
// Primitive slices ([]string, []int, …) are left untouched by this helper —
// defaults on such fields are applied by the value of the field itself, not
// per element.
//
// Note: for scalar fields (including bool) Go cannot distinguish "value was
// explicitly set to the zero value" from "value was not set". If that
// distinction matters, use a pointer type (e.g. *bool) for the field.
func ApplyDefaults(v any) error {
	if v == nil {
		return errors.New("xconfig: ApplyDefaults requires a non-nil value")
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("xconfig: ApplyDefaults requires a non-nil pointer")
	}

	elem := rv.Elem()
	switch elem.Kind() {
	case reflect.Struct:
		fields, err := flat.View(v)
		if err != nil {
			return err
		}
		return applyDefaultsToFields(fields)

	case reflect.Slice:
		elemType := elem.Type().Elem()
		derefType := elemType
		if derefType.Kind() == reflect.Pointer {
			derefType = derefType.Elem()
		}
		if derefType.Kind() != reflect.Struct {
			return nil
		}

		for i := 0; i < elem.Len(); i++ {
			item := elem.Index(i)
			switch item.Kind() {
			case reflect.Pointer:
				if item.IsNil() {
					continue
				}
				if err := ApplyDefaults(item.Interface()); err != nil {
					return err
				}
			default:
				if err := ApplyDefaults(item.Addr().Interface()); err != nil {
					return err
				}
			}
		}
		return nil

	default:
		return errors.New("xconfig: ApplyDefaults requires a pointer to struct or slice of structs")
	}
}

func applyDefaultsToFields(fields flat.Fields) error {
	for _, f := range fields {
		value, ok := f.Tag(defaultTag)
		if !ok {
			continue
		}
		if !f.IsZero() {
			continue
		}
		if err := f.Set(value); err != nil {
			return err
		}
	}
	return nil
}

// GetUnknownFields returns all unknown fields found in configuration files.
// Returns a map where keys are file paths and values are slices of unknown field paths.
// This function is useful for debugging configuration issues or logging warnings about
// extra fields that are not used.
func GetUnknownFields(c Config) map[string][]string {
	if isNilConfig(c) {
		return make(map[string][]string)
	}
	return c.UnknownFields()
}

func (c *config) UnknownFields() map[string][]string {
	if c == nil {
		return make(map[string][]string)
	}
	opts := c.options
	if opts == nil || opts.loader == nil {
		return make(map[string][]string)
	}

	return opts.loader.GetUnknownFields()
}
