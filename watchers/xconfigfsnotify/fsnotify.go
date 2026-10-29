// Package xconfigfsnotify wakes the xconfig refresh loop on file system
// events, so config file changes are applied without waiting for the next
// refresh tick.
//
//	xc, err := xconfig.Load(&cfg,
//		xconfig.WithLoader(l),
//		xconfig.WithFileWatcher(xconfigfsnotify.New()),
//	)
//	results, err := xc.StartRefresh(ctx, time.Minute)
package xconfigfsnotify

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDelay is how long Watch collects events before it notifies.
const DefaultDelay = 100 * time.Millisecond

// Watcher implements xconfig.FileWatcher with fsnotify.
type Watcher struct {
	delay time.Duration
}

// Option configures a Watcher.
type Option func(*Watcher)

// WithDelay sets how long Watch waits after the first event of a burst before
// it notifies once for the whole burst, giving a file written in several steps
// time to be complete. Zero notifies on every event.
func WithDelay(d time.Duration) Option {
	return func(w *Watcher) {
		w.delay = max(d, 0)
	}
}

// New returns a Watcher.
func New(opts ...Option) *Watcher {
	w := &Watcher{delay: DefaultDelay}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Watch implements xconfig.FileWatcher. It watches the directories holding
// paths rather than the files: editors save by renaming a new file over the
// old one and Kubernetes updates a mounted ConfigMap by swapping a symlink in
// the directory, and both end a watch placed on the file itself. For a path
// that is a symlink, the directory of its target is watched as well.
//
// Entries being created, removed, or renamed in a watched directory notify,
// since any of them may replace a file or swap a link, and xconfig compares
// the files before reloading. Writes notify only for the watched files, so a
// busy neighbour such as a log file does not cause reloads. A directory that
// does not exist yet is skipped and left to the refresh ticks.
func (w *Watcher) Watch(ctx context.Context, paths []string, notify func()) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = fw.Close() }()

	watched := watchedFiles(paths)
	for dir := range watched {
		if err := fw.Add(dir); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("watch %s: %w", dir, err)
		}
	}

	// Stopped until the first event; since Go 1.23 a stopped timer never
	// delivers a stale tick. Events while a notification is pending do not
	// push it back, so a steady stream of events cannot postpone it forever.
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	pending := false
	schedule := func() {
		if !pending {
			pending = true
			timer.Reset(w.delay)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-fw.Events:
			if !ok {
				return errors.New("fsnotify: event channel closed")
			}
			if affects(watched, event) {
				schedule()
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return errors.New("fsnotify: error channel closed")
			}
			// Dropped events may have hidden a change, so check the files.
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				schedule()
				continue
			}
			return err
		case <-timer.C:
			pending = false
			notify()
		}
	}
}

func affects(watched map[string]map[string]struct{}, event fsnotify.Event) bool {
	if event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		return true
	}
	if !event.Has(fsnotify.Write) {
		return false
	}
	_, ok := watched[filepath.Dir(event.Name)][filepath.Base(event.Name)]
	return ok
}

// watchedFiles maps each directory to watch to the names of the watched files
// in it.
func watchedFiles(paths []string) map[string]map[string]struct{} {
	watched := make(map[string]map[string]struct{})
	add := func(path string) {
		dir := filepath.Dir(path)
		if watched[dir] == nil {
			watched[dir] = make(map[string]struct{})
		}
		watched[dir][filepath.Base(path)] = struct{}{}
	}

	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		add(abs)
		if info, err := os.Lstat(abs); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			if target, err := filepath.EvalSymlinks(abs); err == nil {
				add(target)
			}
		}
	}
	return watched
}
