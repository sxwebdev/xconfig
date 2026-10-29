package xconfig_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins"
	"github.com/sxwebdev/xconfig/plugins/flag"
	"github.com/sxwebdev/xconfig/plugins/loader"
	"github.com/sxwebdev/xconfig/plugins/validate"
)

type watchConfig struct {
	Host  string            `json:"host" default:"localhost"`
	Port  int               `json:"port" default:"8080"`
	Level string            `json:"level"`
	Tags  map[string]string `json:"tags"`
}

// levelPlugin plays a refreshable source such as Vault: it owns Level and
// outranks the files.
type levelPlugin struct {
	target *watchConfig
	level  atomic.Value
	fail   atomic.Bool
}

func newLevelPlugin(level string) *levelPlugin {
	p := &levelPlugin{}
	p.level.Store(level)
	return p
}

func (p *levelPlugin) Walk(conf any) error {
	p.target = conf.(*watchConfig)
	return nil
}

func (p *levelPlugin) Parse() error {
	p.target.Level = p.level.Load().(string)
	return nil
}

func (p *levelPlugin) Refresh(_ context.Context, target any) (plugins.RefreshOutcome, error) {
	if p.fail.Load() {
		return plugins.RefreshOutcome{}, errors.New("source unavailable")
	}
	conf := target.(*watchConfig)
	level := p.level.Load().(string)
	if conf.Level == level {
		return plugins.RefreshOutcome{}, nil
	}
	conf.Level = level
	return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Level"}}}, nil
}

type fakeWatcher struct {
	err      error
	paths    chan []string
	notifies chan func()
	stopped  atomic.Bool
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{paths: make(chan []string, 1), notifies: make(chan func(), 1)}
}

func (w *fakeWatcher) Watch(ctx context.Context, paths []string, notify func()) error {
	defer w.stopped.Store(true)
	if w.err != nil {
		return w.err
	}
	w.paths <- paths
	w.notifies <- notify
	<-ctx.Done()
	return nil
}

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

// replaceConfigFile renames a fully written file over path, so a refresh
// running concurrently never reads it half written.
func replaceConfigFile(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	writeConfigFile(t, tmp, content)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
}

func newJSONLoader(t *testing.T, path string, optional bool) *loader.Loader {
	t.Helper()
	l, err := loader.NewLoader(map[string]loader.Unmarshal{"json": json.Unmarshal})
	if err != nil {
		t.Fatalf("NewLoader() error = %v", err)
	}
	if err := l.AddFile(path, optional); err != nil {
		t.Fatalf("AddFile() error = %v", err)
	}
	return l
}

// loadWatched loads watchConfig from a JSON file at path with file watching
// enabled, env and flags skipped unless opts bring them back.
func loadWatched(t *testing.T, path string, opts ...xconfig.Option) xconfig.Config {
	t.Helper()
	var conf watchConfig
	base := []xconfig.Option{
		xconfig.WithLoader(newJSONLoader(t, path, false)),
		xconfig.WithSkipFlags(),
		xconfig.WithWatchFiles(),
	}
	manager, err := xconfig.Load(&conf, append(base, opts...)...)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	t.Cleanup(manager.StopRefresh)
	return manager
}

func watchSnapshot(t *testing.T, manager xconfig.Config) watchConfig {
	t.Helper()
	conf, err := xconfig.Snapshot[watchConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	return conf
}

func changeNames(changes []plugins.FieldChange) []string {
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		names = append(names, change.FieldName)
	}
	return names
}

func TestRefreshReloadsChangedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a","port":9000,"tags":{"x":"1"}}`)
	manager := loadWatched(t, path, xconfig.WithSkipEnv())

	writeConfigFile(t, path, `{"host":"b","tags":{"y":"2"}}`)
	result := manager.Refresh(t.Context())
	if result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication without error", result)
	}

	// A key removed from the file falls back to its default, and a map is
	// replaced rather than merged with the previous content.
	want := watchConfig{Host: "b", Port: 8080, Tags: map[string]string{"y": "2"}}
	if got := watchSnapshot(t, manager); !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot() = %+v, want %+v", got, want)
	}
	wantChanges := []string{"Host", "Port", "Tags.x", "Tags.y"}
	if got := changeNames(result.Changes); !reflect.DeepEqual(got, wantChanges) {
		t.Fatalf("Changes = %v, want %v", got, wantChanges)
	}

	if again := manager.Refresh(t.Context()); again.Err != nil || again.Published {
		t.Fatalf("second Refresh() = %+v, want nothing for unchanged files", again)
	}
}

func TestRefreshDoesNotPublishEquivalentFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	manager := loadWatched(t, path, xconfig.WithSkipEnv())

	writeConfigFile(t, path, "{\n  \"host\": \"a\"\n}\n")
	if result := manager.Refresh(t.Context()); result.Err != nil || result.Published {
		t.Fatalf("Refresh() = %+v, want no publication for the same values", result)
	}
}

func TestReloadKeepsEnvPriorityOverFiles(t *testing.T) {
	t.Setenv("XCFGWATCH_HOST", "env-host")

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"file-a","port":1}`)
	manager := loadWatched(t, path, xconfig.WithEnvPrefix("XCFGWATCH"))

	writeConfigFile(t, path, `{"host":"file-b","port":2}`)
	result := manager.Refresh(t.Context())
	if result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication without error", result)
	}

	got := watchSnapshot(t, manager)
	if got.Host != "env-host" || got.Port != 2 {
		t.Fatalf("Snapshot() = %+v, want env host kept and port reloaded", got)
	}
	if names := changeNames(result.Changes); !reflect.DeepEqual(names, []string{"Port"}) {
		t.Fatalf("Changes = %v, want only Port", names)
	}
}

func TestFailedReloadKeepsSnapshotAndRetries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	manager := loadWatched(t, path, xconfig.WithSkipEnv())

	writeConfigFile(t, path, `{"host":`)
	for range 2 {
		result := manager.Refresh(t.Context())
		if result.Err == nil || result.Published {
			t.Fatalf("Refresh() = %+v, want an error for a broken file", result)
		}
		if got := watchSnapshot(t, manager); got.Host != "a" {
			t.Fatalf("Snapshot().Host = %q after a failed reload, want %q", got.Host, "a")
		}
	}

	writeConfigFile(t, path, `{"host":"b"}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v after fixing the file, want a publication", result)
	}
	if got := watchSnapshot(t, manager); got.Host != "b" {
		t.Fatalf("Snapshot().Host = %q, want %q", got.Host, "b")
	}
}

func TestReloadRunsValidation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"port":80}`)
	errInvalidPort := errors.New("invalid port")
	manager := loadWatched(t, path, xconfig.WithSkipEnv(), xconfig.WithPlugins(validate.New(func(v any) error {
		if v.(*watchConfig).Port > 65535 {
			return errInvalidPort
		}
		return nil
	})))

	writeConfigFile(t, path, `{"port":70000}`)
	result := manager.Refresh(t.Context())
	if !errors.Is(result.Err, errInvalidPort) || result.Published {
		t.Fatalf("Refresh() = %+v, want the validation error", result)
	}
	if got := watchSnapshot(t, manager); got.Port != 80 {
		t.Fatalf("Snapshot().Port = %d after a rejected reload, want 80", got.Port)
	}
}

func TestReloadRefreshesPluginsOverFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a","level":"file"}`)
	source := newLevelPlugin("plugin")
	manager := loadWatched(t, path, xconfig.WithSkipEnv(), xconfig.WithPlugins(source))

	writeConfigFile(t, path, `{"host":"b","level":"file"}`)
	result := manager.Refresh(t.Context())
	if result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication without error", result)
	}
	if got := watchSnapshot(t, manager); got.Host != "b" || got.Level != "plugin" {
		t.Fatalf("Snapshot() = %+v, want reloaded host and plugin level", got)
	}
	if names := changeNames(result.Changes); !reflect.DeepEqual(names, []string{"Host"}) {
		t.Fatalf("Changes = %v, want only Host", names)
	}

	// A failing source fails the reload as a whole.
	source.fail.Store(true)
	writeConfigFile(t, path, `{"host":"c"}`)
	if result := manager.Refresh(t.Context()); result.Err == nil || result.Published {
		t.Fatalf("Refresh() = %+v, want an error while the source fails", result)
	}
	source.fail.Store(false)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v once the source recovers, want a publication", result)
	}
	if got := watchSnapshot(t, manager); got.Host != "c" {
		t.Fatalf("Snapshot().Host = %q, want %q", got.Host, "c")
	}
}

func TestBrokenFileDoesNotBlockPluginRefresh(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	source := newLevelPlugin("v1")
	manager := loadWatched(t, path, xconfig.WithSkipEnv(), xconfig.WithPlugins(source))

	writeConfigFile(t, path, `{"host":`)
	source.level.Store("v2")
	result := manager.Refresh(t.Context())
	if result.Err == nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want the file error and the plugin change", result)
	}
	if got := watchSnapshot(t, manager); got.Host != "a" || got.Level != "v2" {
		t.Fatalf("Snapshot() = %+v, want the old host and the rotated level", got)
	}
}

func TestReloadUpdatesUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a","extra":1}`)
	manager := loadWatched(t, path, xconfig.WithSkipEnv())
	if got := xconfig.GetUnknownFields(manager)[path]; !reflect.DeepEqual(got, []string{"extra"}) {
		t.Fatalf("GetUnknownFields() = %v, want [extra]", got)
	}

	writeConfigFile(t, path, `{"host":"a"}`)
	if result := manager.Refresh(t.Context()); result.Err != nil {
		t.Fatalf("Refresh() error = %v", result.Err)
	}
	if got := xconfig.GetUnknownFields(manager); len(got) != 0 {
		t.Fatalf("GetUnknownFields() = %v after removing the key, want none", got)
	}
}

func TestReloadOptionalFileAppearsAndDisappears(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	var conf watchConfig
	manager, err := xconfig.Load(&conf,
		xconfig.WithLoader(newJSONLoader(t, path, true)),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	writeConfigFile(t, path, `{"host":"a"}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v after creating the file, want a publication", result)
	}
	if got := watchSnapshot(t, manager); got.Host != "a" {
		t.Fatalf("Snapshot().Host = %q, want %q", got.Host, "a")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v after removing the file, want a publication", result)
	}
	if got := watchSnapshot(t, manager); got.Host != "localhost" {
		t.Fatalf("Snapshot().Host = %q, want the default", got.Host)
	}
}

func TestReloadRebindsPassedPlugins(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a","port":1}`)
	manager := loadWatched(t, path, xconfig.WithSkipEnv(), xconfig.WithPlugins(
		// A FlagSet panics on a flag defined twice, and a reader is consumed
		// by the first parse; both must survive any number of reloads.
		flag.New("app", flag.ContinueOnError, []string{"-port=1234"}),
		loader.NewReader(strings.NewReader(`{"level":"embedded"}`), json.Unmarshal),
	))

	for _, host := range []string{"b", "c"} {
		writeConfigFile(t, path, `{"host":"`+host+`","port":2}`)
		if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
			t.Fatalf("Refresh() = %+v, want a publication", result)
		}
		want := watchConfig{Host: host, Port: 1234, Level: "embedded"}
		if got := watchSnapshot(t, manager); !reflect.DeepEqual(got, want) {
			t.Fatalf("Snapshot() = %+v, want %+v", got, want)
		}
	}
}

func TestReloadStartsFromValueBeforeParse(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	conf := watchConfig{Level: "preset"}
	manager, err := xconfig.Load(&conf,
		xconfig.WithLoader(newJSONLoader(t, path, false)),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	conf.Level = "changed after load"

	writeConfigFile(t, path, `{"host":"b"}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication", result)
	}
	if got := watchSnapshot(t, manager); got.Level != "preset" || got.Host != "b" {
		t.Fatalf("Snapshot() = %+v, want the preset level and the reloaded host", got)
	}
}

func TestWatchFilesWithoutFilesIsNotRefreshable(t *testing.T) {
	t.Parallel()

	var conf watchConfig
	manager, err := xconfig.Load(&conf, xconfig.WithSkipFlags(), xconfig.WithSkipEnv(), xconfig.WithWatchFiles())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, err := manager.StartRefresh(t.Context(), time.Hour); !errors.Is(err, xconfig.ErrNoRefreshablePlugins) {
		t.Fatalf("StartRefresh() error = %v, want ErrNoRefreshablePlugins", err)
	}
}

func TestStartRefreshReloadsOnWatcherNotify(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	watcher := newFakeWatcher()
	manager := loadWatched(t, path, xconfig.WithSkipEnv(), xconfig.WithFileWatcher(watcher))

	// No refreshable plugin is needed once files are watched, and the hour
	// long interval leaves the watcher as the only trigger.
	results, err := manager.StartRefresh(t.Context(), time.Hour)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	if got := <-watcher.paths; !reflect.DeepEqual(got, []string{path}) {
		t.Fatalf("watched paths = %v, want [%s]", got, path)
	}
	notify := <-watcher.notifies

	// Readers keep taking snapshots while the reload publishes.
	stopReading, readersDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(readersDone)
		for {
			select {
			case <-stopReading:
				return
			default:
			}
			if _, err := xconfig.Snapshot[watchConfig](manager); err != nil {
				t.Errorf("Snapshot() error = %v", err)
				return
			}
		}
	}()
	defer func() {
		close(stopReading)
		<-readersDone
	}()

	writeConfigFile(t, path, `{"host":"b"}`)
	notify()
	notify() // a burst must not block the watcher

	select {
	case result := <-results:
		if result.Err != nil || !result.Published {
			t.Fatalf("result = %+v, want a publication", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no refresh result after the watcher notified")
	}
	if got := watchSnapshot(t, manager); got.Host != "b" {
		t.Fatalf("Snapshot().Host = %q, want %q", got.Host, "b")
	}

	manager.StopRefresh()
	if !watcher.stopped.Load() {
		t.Fatal("StopRefresh() returned before the watcher stopped")
	}
}

func TestStartRefreshReportsWatcherError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	errWatch := errors.New("watch failed")
	watcher := newFakeWatcher()
	watcher.err = errWatch
	manager := loadWatched(t, path, xconfig.WithSkipEnv(), xconfig.WithFileWatcher(watcher))

	results, err := manager.StartRefresh(t.Context(), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}

	select {
	case result := <-results:
		if !errors.Is(result.Err, errWatch) {
			t.Fatalf("result.Err = %v, want the watcher error", result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher error was not reported")
	}

	// Ticks keep reloading after the watcher has failed.
	replaceConfigFile(t, path, `{"host":"b"}`)
	select {
	case result := <-results:
		if result.Err != nil || !result.Published {
			t.Fatalf("result = %+v, want a publication", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reload after the watcher failed")
	}
}

func TestLoadWithWatchFilesRequiresFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	var conf watchConfig
	_, err := xconfig.Load(&conf,
		xconfig.WithLoader(newJSONLoader(t, path, false)),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
	)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load() error = %v, want fs.ErrNotExist", err)
	}
}

func TestParseAgainIsRejectedWhenWatching(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	manager := loadWatched(t, path, xconfig.WithSkipEnv())

	writeConfigFile(t, path, `{"host":"b"}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication", result)
	}
	if err := manager.Parse(); !errors.Is(err, xconfig.ErrWatchedReparse) {
		t.Fatalf("Parse() error = %v, want ErrWatchedReparse", err)
	}
	if got := watchSnapshot(t, manager); got.Host != "b" {
		t.Fatalf("Snapshot().Host = %q after a rejected Parse(), want %q", got.Host, "b")
	}
}

// presenceGuard records the present fields it reads from the loader in every
// Parse and Refresh, as a plugin rejecting keys that must not come from files
// would.
type presenceGuard struct {
	present func() map[string]struct{}
	seen    [][]string
}

func (*presenceGuard) Walk(any) error { return nil }

func (g *presenceGuard) Parse() error {
	g.seen = append(g.seen, slices.Sorted(maps.Keys(g.present())))
	return nil
}

func (g *presenceGuard) lastSeen() []string { return g.seen[len(g.seen)-1] }

// refreshingPresenceGuard also takes part in refreshes between reloads.
type refreshingPresenceGuard struct{ presenceGuard }

func (g *refreshingPresenceGuard) Refresh(context.Context, any) (plugins.RefreshOutcome, error) {
	return plugins.RefreshOutcome{}, g.Parse()
}

func TestPluginsSeePresentFieldsOfEachParse(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	l := newJSONLoader(t, path, false)
	guard := &presenceGuard{present: l.PresentFields}
	var conf watchConfig
	manager, err := xconfig.Load(&conf,
		xconfig.WithLoader(l),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
		xconfig.WithPlugins(guard),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := guard.lastSeen(); !slices.Equal(got, []string{"host"}) {
		t.Fatalf("present fields at Load = %v, want [host]", got)
	}

	writeConfigFile(t, path, `{"host":"a","port":1}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication", result)
	}
	if got := guard.lastSeen(); !slices.Equal(got, []string{"host", "port"}) {
		t.Fatalf("present fields at reload = %v, want [host port]", got)
	}
}

func TestRejectedReloadKeepsPresentFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	l := newJSONLoader(t, path, false)
	guard := &refreshingPresenceGuard{presenceGuard{present: l.PresentFields}}
	errRejected := errors.New("port rejected")
	var conf watchConfig
	manager, err := xconfig.Load(&conf,
		xconfig.WithLoader(l),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
		xconfig.WithPlugins(guard, validate.New(func(v any) error {
			if v.(*watchConfig).Port == 666 {
				return errRejected
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	for _, broken := range []string{`{"host":"a","port":666}`, `{"host":`} {
		writeConfigFile(t, path, broken)
		if result := manager.Refresh(t.Context()); result.Err == nil || result.Published {
			t.Fatalf("Refresh() with %s = %+v, want a rejected reload", broken, result)
		}
		if got := slices.Sorted(maps.Keys(l.PresentFields())); !slices.Equal(got, []string{"host"}) {
			t.Fatalf("Loader.PresentFields() after a rejected reload = %v, want [host]", got)
		}
		// The refresh that follows a rejected reload in the same cycle sees
		// the files behind the snapshot, not the rejected ones.
		if got := guard.lastSeen(); !slices.Equal(got, []string{"host"}) {
			t.Fatalf("present fields after a rejected reload = %v, want [host]", got)
		}
	}

	writeConfigFile(t, path, `{"host":"a","port":1}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication", result)
	}
	if got := guard.lastSeen(); !slices.Equal(got, []string{"host", "port"}) {
		t.Fatalf("present fields at the accepted reload = %v, want [host port]", got)
	}
	if got := slices.Sorted(maps.Keys(l.PresentFields())); !slices.Equal(got, []string{"host", "port"}) {
		t.Fatalf("Loader.PresentFields() = %v, want [host port]", got)
	}
}

func TestPluginsSeePresentFieldsWithoutWatching(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a"}`)
	l := newJSONLoader(t, path, false)
	guard := &presenceGuard{present: l.PresentFields}
	var conf watchConfig
	if _, err := xconfig.Load(&conf, xconfig.WithLoader(l), xconfig.WithSkipFlags(), xconfig.WithSkipEnv(),
		xconfig.WithPlugins(guard)); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := guard.lastSeen(); !slices.Equal(got, []string{"host"}) {
		t.Fatalf("present fields = %v, want [host]", got)
	}
}

// loadTwoFiles loads watchConfig from first and second, in that order, with
// file watching enabled.
func loadTwoFiles(t *testing.T, first, second string, secondOptional bool) (xconfig.Config, *loader.Loader) {
	t.Helper()
	l := newJSONLoader(t, first, false)
	if err := l.AddFile(second, secondOptional); err != nil {
		t.Fatalf("AddFile() error = %v", err)
	}
	var conf watchConfig
	manager, err := xconfig.Load(&conf,
		xconfig.WithLoader(l),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return manager, l
}

func presentFields(l *loader.Loader) []string {
	return slices.Sorted(maps.Keys(l.PresentFields()))
}

func TestReloadDropsPresentFieldsOfRemovedOptionalFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base, extra := filepath.Join(dir, "base.json"), filepath.Join(dir, "extra.json")
	writeConfigFile(t, base, `{"host":"a"}`)
	writeConfigFile(t, extra, `{"port":1}`)
	manager, l := loadTwoFiles(t, base, extra, true)
	if got := presentFields(l); !slices.Equal(got, []string{"host", "port"}) {
		t.Fatalf("PresentFields() at Load = %v, want [host port]", got)
	}

	if err := os.Remove(extra); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication", result)
	}
	if got := presentFields(l); !slices.Equal(got, []string{"host"}) {
		t.Fatalf("PresentFields() after removing the optional file = %v, want [host]", got)
	}
}

func TestReloadFailingOnSecondFileRestoresPresentFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
	writeConfigFile(t, first, `{"host":"a"}`)
	writeConfigFile(t, second, `{"port":1}`)
	manager, l := loadTwoFiles(t, first, second, false)

	// The first file parses and records its fields before the second fails.
	writeConfigFile(t, first, `{"host":"a","level":"debug"}`)
	writeConfigFile(t, second, `{"port":`)
	if result := manager.Refresh(t.Context()); result.Err == nil || result.Published {
		t.Fatalf("Refresh() = %+v, want a rejected reload", result)
	}
	if got := presentFields(l); !slices.Equal(got, []string{"host", "port"}) {
		t.Fatalf("PresentFields() after the rejected reload = %v, want the accepted [host port]", got)
	}
}

func TestUnknownFieldsReportAcceptedFilesDuringReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, path, `{"host":"a","extra":1}`)
	l := newJSONLoader(t, path, false)

	// The plugin reads both views in the middle of every parse, as a reader on
	// another goroutine could while a reload runs.
	var (
		manager       xconfig.Config
		duringConfig  map[string][]string
		duringLoader  map[string][]string
		errRejectPort = errors.New("port rejected")
	)
	probe := &parsePlugin{parse: func() error {
		if manager != nil {
			duringConfig, duringLoader = manager.UnknownFields(), l.GetUnknownFields()
		}
		return nil
	}}
	var conf watchConfig
	manager, err := xconfig.Load(&conf,
		xconfig.WithLoader(l),
		xconfig.WithSkipFlags(),
		xconfig.WithSkipEnv(),
		xconfig.WithWatchFiles(),
		xconfig.WithPlugins(probe, validate.New(func(v any) error {
			if v.(*watchConfig).Port == 666 {
				return errRejectPort
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	accepted := map[string][]string{path: {"extra"}}
	if got := manager.UnknownFields(); !reflect.DeepEqual(got, accepted) {
		t.Fatalf("UnknownFields() after Load = %v, want %v", got, accepted)
	}

	writeConfigFile(t, path, `{"host":"a","other":1,"port":666}`)
	if result := manager.Refresh(t.Context()); !errors.Is(result.Err, errRejectPort) {
		t.Fatalf("Refresh() error = %v, want the rejection", result.Err)
	}
	if want := map[string][]string{path: {"other"}}; !reflect.DeepEqual(duringLoader, want) {
		t.Fatalf("Loader.GetUnknownFields() during the reload = %v, want %v", duringLoader, want)
	}
	if !reflect.DeepEqual(duringConfig, accepted) {
		t.Fatalf("UnknownFields() during the reload = %v, want the accepted %v", duringConfig, accepted)
	}
	if got := manager.UnknownFields(); !reflect.DeepEqual(got, accepted) {
		t.Fatalf("UnknownFields() after the rejected reload = %v, want %v", got, accepted)
	}

	writeConfigFile(t, path, `{"host":"b","other":1}`)
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a publication", result)
	}
	if want := map[string][]string{path: {"other"}}; !reflect.DeepEqual(manager.UnknownFields(), want) {
		t.Fatalf("UnknownFields() after the accepted reload = %v, want %v", manager.UnknownFields(), want)
	}
}
