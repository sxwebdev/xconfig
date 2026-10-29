package xconfigfsnotify_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/loader"
	"github.com/sxwebdev/xconfig/watchers/xconfigfsnotify"
)

const eventTimeout = 5 * time.Second

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

// replaceFile writes content next to path and renames it over path, so a
// concurrent reader never sees a partly written file.
func replaceFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// startWatch runs Watch until the test ends and returns a channel receiving a
// value per notification. fsnotify has no readiness signal, so it gives the
// watcher a moment to register before the test changes files.
func startWatch(t *testing.T, w *xconfigfsnotify.Watcher, paths ...string) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	notified := make(chan struct{}, 64)
	done := make(chan error, 1)
	go func() {
		done <- w.Watch(ctx, paths, func() { notified <- struct{}{} })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Watch() error = %v", err)
		}
	})
	time.Sleep(50 * time.Millisecond)
	return notified
}

func waitNotified(t *testing.T, notified <-chan struct{}) {
	t.Helper()
	select {
	case <-notified:
	case <-time.After(eventTimeout):
		t.Fatal("Watch() did not notify")
	}
}

func TestWatchNotifiesOnWrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{}`)
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(0)), path)

	writeFile(t, path, `{"host":"b"}`)
	waitNotified(t, notified)
}

func TestWatchNotifiesOnRenameOver(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{}`)
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(0)), path)

	// Editors save by writing a temporary file and renaming it over the
	// original, which ends a watch on the original file.
	for range 2 {
		tmp := filepath.Join(dir, ".config.json.tmp")
		writeFile(t, tmp, `{"host":"b"}`)
		if err := os.Rename(tmp, path); err != nil {
			t.Fatalf("Rename() error = %v", err)
		}
		waitNotified(t, notified)
		for len(notified) > 0 {
			<-notified
		}
	}
}

func TestWatchNotifiesOnSymlinkSwap(t *testing.T) {
	t.Parallel()

	// The layout of a Kubernetes ConfigMap volume: config.json links through
	// ..data, which an update atomically points at a new directory.
	dir := t.TempDir()
	for _, version := range []string{"v1", "v2"} {
		if err := os.Mkdir(filepath.Join(dir, version), 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v", err)
		}
		writeFile(t, filepath.Join(dir, version, "config.json"), `{"version":"`+version+`"}`)
	}
	if err := os.Symlink("v1", filepath.Join(dir, "..data")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.Symlink(filepath.Join("..data", "config.json"), path); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(0)), path)

	if err := os.Symlink("v2", filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	// The kubelet then removes the previous version. inotify already reports
	// the rename, while kqueue only reports this removal, from the watch on
	// the directory the link resolved to.
	if err := os.RemoveAll(filepath.Join(dir, "v1")); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	waitNotified(t, notified)
}

func TestWatchFollowsSymlinkTarget(t *testing.T) {
	t.Parallel()

	// A link into another directory, whose target is edited in place. inotify
	// sees this only through the watch on the target's directory; kqueue also
	// follows the link from the watch on the link's own directory.
	target := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, target, `{}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(0)), path)

	writeFile(t, target, `{"host":"b"}`)
	waitNotified(t, notified)
}

func TestWatchNotifiesOncePerBurst(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{}`)
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(300*time.Millisecond)), path)

	for range 10 {
		writeFile(t, path, `{"host":"b"}`)
		time.Sleep(10 * time.Millisecond)
	}
	waitNotified(t, notified)
	time.Sleep(500 * time.Millisecond)
	if extra := len(notified); extra != 0 {
		t.Fatalf("Watch() notified %d more times for one burst, want once", extra+1)
	}
}

func TestWatchIgnoresWritesToOtherFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{}`)
	neighbour := filepath.Join(dir, "app.log")
	writeFile(t, neighbour, "")
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(0)), path)

	for range 5 {
		writeFile(t, neighbour, "line\n")
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(notified); n != 0 {
		t.Fatalf("Watch() notified %d times for writes to another file, want none", n)
	}

	writeFile(t, path, `{"host":"b"}`)
	waitNotified(t, notified)
}

func TestWatchNotifiesDuringSteadyEvents(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{}`)
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(100*time.Millisecond)), path)

	// Events keep arriving for far longer than the delay; the notification
	// must still come while they do.
	stop := time.After(3 * time.Second)
	for {
		select {
		case <-notified:
			return
		case <-stop:
			t.Fatal("Watch() did not notify while events kept arriving")
		default:
			writeFile(t, path, `{"host":"b"}`)
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestWatchSkipsMissingDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	present := filepath.Join(dir, "config.json")
	writeFile(t, present, `{}`)
	notified := startWatch(t, xconfigfsnotify.New(xconfigfsnotify.WithDelay(0)),
		filepath.Join(dir, "missing", "config.json"), present)

	writeFile(t, present, `{"host":"b"}`)
	waitNotified(t, notified)
}

type serverConfig struct {
	Host string `json:"host" default:"localhost"`
}

func TestLoadAppliesFileChangeOnEvent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"host":"a"}`)
	l, err := loader.NewLoader(map[string]loader.Unmarshal{"json": json.Unmarshal})
	if err != nil {
		t.Fatalf("NewLoader() error = %v", err)
	}
	if err := l.AddFile(path, false); err != nil {
		t.Fatalf("AddFile() error = %v", err)
	}

	var conf serverConfig
	xc, err := xconfig.Load(&conf,
		xconfig.WithLoader(l),
		xconfig.WithSkipEnv(),
		xconfig.WithSkipFlags(),
		xconfig.WithFileWatcher(xconfigfsnotify.New()),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// The interval is far beyond the timeout, so only an event can reload.
	results, err := xc.StartRefresh(t.Context(), time.Hour)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	t.Cleanup(xc.StopRefresh)

	var stop atomic.Bool
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		// Keep rewriting until the reload lands, in case the first write
		// raced the watcher's registration.
		for !stop.Load() {
			_ = replaceFile(path, `{"host":"b"}`)
			time.Sleep(200 * time.Millisecond)
		}
	}()
	defer func() {
		stop.Store(true)
		<-writerDone
	}()

	select {
	case result := <-results:
		if result.Err != nil || !result.Published {
			t.Fatalf("result = %+v, want a publication", result)
		}
	case <-time.After(eventTimeout):
		t.Fatal("file change was not applied")
	}
	got, err := xconfig.Snapshot[serverConfig](xc)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got.Host != "b" {
		t.Fatalf("Snapshot().Host = %q, want %q", got.Host, "b")
	}
}
