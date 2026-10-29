package xconfig

import (
	"context"
	"fmt"
	"reflect"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

// FileWatcher wakes the StartRefresh loop when config files may have changed.
// See WithFileWatcher; xconfigfsnotify provides one based on file system
// events.
type FileWatcher interface {
	// Watch watches paths and calls notify whenever any of them may have
	// changed. Spurious calls are harmless because the files are compared
	// before reloading, and notify never blocks. Watch must return once ctx is
	// done; an error returned before that stops the watcher and is reported as
	// a RefreshResult while ticks keep checking the files.
	Watch(ctx context.Context, paths []string, notify func()) error
}

type fileWatch struct {
	baseline any // the value passed to Load, as it was before parsing
	paths    []string
	watcher  FileWatcher

	applied *loader.FileSet // the files behind the published snapshot

	// unknown holds the unknown fields of applied, guarded by config.dataMu.
	// The loader reports those of a reload in progress instead.
	unknown map[string][]string
}

// newFileWatch reads the loader's files for the initial parse, so the files a
// later refresh compares against are exactly the ones that were parsed. It
// returns nil when the loader has no files.
func newFileWatch(conf any, o *options) (*fileWatch, error) {
	files, err := o.loader.ReadFiles()
	if err != nil {
		return nil, err
	}
	paths := files.Paths()
	if len(paths) == 0 {
		return nil, nil
	}

	baseline, err := cloneConfigPointer(conf)
	if err != nil {
		return nil, fmt.Errorf("xconfig: keep configuration for file reloads: %w", err)
	}

	return &fileWatch{
		baseline: baseline,
		paths:    paths,
		watcher:  o.fileWatcher,
		applied:  files,
	}, nil
}

// run starts the watcher. The first channel receives a value whenever a file
// may have changed and the second the error the watcher stopped with; wait
// returns once the watcher has stopped after ctx is done.
func (w *fileWatch) run(ctx context.Context) (wake <-chan struct{}, errs <-chan error, wait func()) {
	wakeCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	stopped := make(chan struct{})
	notify := func() {
		select {
		case wakeCh <- struct{}{}:
		default:
		}
	}

	go func() {
		defer close(stopped)
		if err := w.watcher.Watch(ctx, w.paths, notify); err != nil && ctx.Err() == nil {
			errCh <- err
		}
	}()

	return wakeCh, errCh, func() { <-stopped }
}

// reloadFiles reloads the configuration when the files differ from the ones
// behind current. It reports whether it did, in which case the refreshable
// plugins have already been refreshed as part of the reload. A failed reload
// changes nothing and is retried by the next refresh.
func (c *config) reloadFiles(ctx context.Context, current any, result *RefreshResult) (bool, error) {
	files, err := c.options.loader.ReadFiles()
	if err != nil {
		return false, fmt.Errorf("reload config files: %w", err)
	}
	if files.Equal(c.watch.applied) {
		return false, nil
	}

	filePlugins, restore := files.Plugins()
	staging, warnings, err := c.reload(ctx, filePlugins)
	if err != nil {
		restore()
		return false, fmt.Errorf("reload config files: %w", err)
	}

	if !sameConfigData(staging, current) {
		changes, err := changedFields(current, staging)
		if err != nil {
			restore()
			return false, fmt.Errorf("reload config files: %w", err)
		}
		published, err := cloneConfigPointer(staging)
		if err != nil {
			restore()
			return false, fmt.Errorf("publish reloaded configuration: %w", err)
		}
		c.publish(published)
		result.Changes = changes
		result.Published = true
	}

	result.Warnings = append(result.Warnings, warnings...)
	c.staging = staging
	c.watch.applied = files
	c.acceptUnknownFields()
	return true, nil
}

// acceptUnknownFields records the unknown fields the loader holds as those of
// the accepted files.
func (c *config) acceptUnknownFields() {
	unknown := c.options.loader.GetUnknownFields()

	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	c.watch.unknown = unknown
}

// reload runs the Load chain with the file plugins files on a fresh copy of
// the baseline. Plugins passed with WithPlugins are bound to that copy again,
// except refreshable ones, which refresh it in their place in the chain.
func (c *config) reload(ctx context.Context, files []plugins.Plugin) (any, []error, error) {
	target, err := cloneConfigPointer(c.watch.baseline)
	if err != nil {
		return nil, nil, err
	}
	fields, err := flat.View(target)
	if err != nil {
		return nil, nil, err
	}

	ps := c.options.chain(files)
	for _, p := range ps {
		if _, ok := p.(plugins.Refreshable); ok {
			continue
		}
		if err := bindPlugin(p, target, fields); err != nil {
			return nil, nil, err
		}
	}

	var warnings []error
	for _, p := range ps {
		refreshable, ok := p.(plugins.Refreshable)
		if !ok {
			if err := p.Parse(); err != nil {
				return nil, nil, err
			}
			continue
		}
		outcome, err := refreshable.Refresh(ctx, target)
		warnings = append(warnings, outcome.Warnings...)
		if err != nil {
			return nil, nil, fmt.Errorf("refresh %T: %w", p, err)
		}
	}

	return target, warnings, nil
}

// changedFields lists the flat fields whose values differ between two
// configurations, including entries present in only one of them.
func changedFields(before, after any) ([]plugins.FieldChange, error) {
	beforeFields, err := flat.View(before)
	if err != nil {
		return nil, err
	}
	afterFields, err := flat.View(after)
	if err != nil {
		return nil, err
	}

	values := make(map[string]reflect.Value, len(beforeFields))
	for _, f := range beforeFields {
		values[f.Name()] = f.FieldValue()
	}

	changed := make(map[string]struct{})
	for _, f := range afterFields {
		old, ok := values[f.Name()]
		delete(values, f.Name())
		if ok && sameValue(old, f.FieldValue(), make(map[comparePair]struct{})) {
			continue
		}
		changed[f.Name()] = struct{}{}
	}
	for name := range values {
		changed[name] = struct{}{}
	}

	changes := make([]plugins.FieldChange, 0, len(changed))
	for name := range changed {
		changes = append(changes, plugins.FieldChange{FieldName: name})
	}
	sortFieldChanges(changes)
	return changes, nil
}
