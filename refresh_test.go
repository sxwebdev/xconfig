package xconfig_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins"
)

type refreshConfig struct {
	Version  int
	Values   map[string]int
	Pointer  *int
	Endpoint *url.URL
	private  map[string]int
}

type refreshPlugin struct {
	refresh func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error)
}

// parsePlugin is a plugin without refresh support whose Parse hook is
// controlled by the test.
type parsePlugin struct {
	parse func() error
}

// noopRefreshPlugin refreshes any config type without ever reporting a change.
type noopRefreshPlugin struct {
	refresh func(target any)
}

func (*noopRefreshPlugin) Walk(any) error { return nil }

func (*noopRefreshPlugin) Parse() error { return nil }

func (p *noopRefreshPlugin) Refresh(_ context.Context, target any) (plugins.RefreshOutcome, error) {
	if p.refresh != nil {
		p.refresh(target)
	}
	return plugins.RefreshOutcome{}, nil
}

func (*parsePlugin) Walk(any) error { return nil }

func (p *parsePlugin) Parse() error {
	if p.parse == nil {
		return nil
	}
	return p.parse()
}

func (*refreshPlugin) Walk(any) error { return nil }

func (p *refreshPlugin) Parse() error { return nil }

func (p *refreshPlugin) Refresh(ctx context.Context, target any) (plugins.RefreshOutcome, error) {
	config, ok := target.(*refreshConfig)
	if !ok {
		return plugins.RefreshOutcome{}, fmt.Errorf("unexpected config type %T", target)
	}
	return p.refresh(ctx, config)
}

func TestRefreshPublishesOwnedSnapshot(t *testing.T) {
	t.Parallel()

	initialPointer := 1
	initial := refreshConfig{
		Version: 1,
		Values:  map[string]int{"version": 1},
		Pointer: &initialPointer,
	}
	plugin := &refreshPlugin{
		refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
			pointer := 2
			config.Version = 2
			config.Values = map[string]int{"version": 2}
			config.Pointer = &pointer
			return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Version"}}}, nil
		},
	}

	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result := manager.Refresh(t.Context()); result.Err != nil {
		t.Fatalf("Refresh() error = %v", result.Err)
	}

	if initial.Version != 1 || initial.Values["version"] != 1 || *initial.Pointer != 1 {
		t.Fatalf("Refresh() mutated caller-owned initial config: %+v", initial)
	}

	snapshot, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Version != 2 || snapshot.Values["version"] != 2 || *snapshot.Pointer != 2 {
		t.Fatalf("Snapshot() = %+v, want version 2", snapshot)
	}

	snapshot.Values["version"] = 99
	*snapshot.Pointer = 99
	another, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}
	if another.Values["version"] != 2 || *another.Pointer != 2 {
		t.Fatalf("Snapshot() shares mutable state: %+v", another)
	}
}

func TestRefreshDoesNotPublishPartialFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("source unavailable")
	initial := refreshConfig{Version: 1}
	first := true
	change := &refreshPlugin{
		refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
			if config.Version == 1 {
				config.Version = 2
				return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Version"}}}, nil
			}
			return plugins.RefreshOutcome{}, nil
		},
	}
	failure := &refreshPlugin{
		refresh: func(_ context.Context, _ *refreshConfig) (plugins.RefreshOutcome, error) {
			if first {
				first = false
				return plugins.RefreshOutcome{}, sentinel
			}
			return plugins.RefreshOutcome{}, nil
		},
	}

	manager, err := xconfig.Custom(&initial, change, failure)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if result := manager.Refresh(t.Context()); !errors.Is(result.Err, sentinel) {
		t.Fatalf("first Refresh() error = %v, want %v", result.Err, sentinel)
	}
	beforeRetry, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if beforeRetry.Version != 1 {
		t.Fatalf("failed Refresh() published Version = %d, want 1", beforeRetry.Version)
	}

	result := manager.Refresh(t.Context())
	if result.Err != nil {
		t.Fatalf("second Refresh() error = %v", result.Err)
	}
	if len(result.Changes) != 1 || result.Changes[0].FieldName != "Version" {
		t.Fatalf("second Refresh() changes = %+v, want pending Version change", result.Changes)
	}
	afterRetry, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if afterRetry.Version != 2 {
		t.Fatalf("successful Refresh() Version = %d, want 2", afterRetry.Version)
	}
}

func TestConcurrentRefreshAndSnapshot(t *testing.T) {
	t.Parallel()

	pointer := 0
	initial := refreshConfig{Values: map[string]int{"version": 0}, Pointer: &pointer}
	plugin := &refreshPlugin{
		refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
			config.Version++
			version := config.Version
			config.Values = map[string]int{"version": version}
			config.Pointer = &version
			return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Version"}}}, nil
		},
	}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	const refreshes = 500
	const readers = 4
	done := make(chan struct{})
	errorsFound := make(chan error, readers+1)
	var readersWG sync.WaitGroup
	for range readers {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				snapshot, snapshotErr := xconfig.Snapshot[refreshConfig](manager)
				if snapshotErr != nil {
					errorsFound <- snapshotErr
					return
				}
				if snapshot.Pointer == nil || snapshot.Values["version"] != snapshot.Version || *snapshot.Pointer != snapshot.Version {
					errorsFound <- fmt.Errorf("inconsistent snapshot: %+v", snapshot)
					return
				}
			}
		}()
	}

	for range refreshes {
		if result := manager.Refresh(t.Context()); result.Err != nil {
			errorsFound <- result.Err
			break
		}
	}
	close(done)
	readersWG.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent operation failed: %v", err)
	}
}

func TestStartRefreshReplacesRunningLoop(t *testing.T) {
	t.Parallel()

	initial := refreshConfig{}
	var calls atomic.Int64
	plugin := &refreshPlugin{
		refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
			version := int(calls.Add(1))
			config.Version = version
			return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Version"}}}, nil
		},
	}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	firstResults, err := manager.StartRefresh(t.Context(), time.Millisecond)
	if err != nil {
		t.Fatalf("first StartRefresh() error = %v", err)
	}
	select {
	case result := <-firstResults:
		if result.Err != nil {
			t.Fatalf("first refresh result error = %v", result.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("first refresh loop did not run")
	}

	secondResults, err := manager.StartRefresh(t.Context(), time.Millisecond)
	if err != nil {
		t.Fatalf("second StartRefresh() error = %v", err)
	}
	select {
	case result := <-secondResults:
		if result.Err != nil {
			t.Fatalf("replacement refresh result error = %v", result.Err)
		}
		if _, snapshotErr := xconfig.Snapshot[refreshConfig](manager); snapshotErr != nil {
			t.Fatalf("Snapshot() after refresh error = %v", snapshotErr)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement refresh loop did not run")
	}
	manager.StopRefresh()
	manager.StopRefresh()
}

func TestFailedMutationIsDiscardedWithoutEmptyPublication(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("refresh interrupted")
	initial := refreshConfig{Version: 1}
	first := true
	plugin := &refreshPlugin{
		refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
			if first {
				first = false
				config.Version = 2
				return plugins.RefreshOutcome{}, sentinel
			}
			return plugins.RefreshOutcome{}, nil
		},
	}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if result := manager.Refresh(t.Context()); !errors.Is(result.Err, sentinel) {
		t.Fatalf("first Refresh() error = %v, want %v", result.Err, sentinel)
	}
	if result := manager.Refresh(t.Context()); result.Err != nil || result.Published {
		t.Fatalf("second Refresh() = %+v, want successful no-op", result)
	}

	snapshot, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("Snapshot() Version = %d, want 1", snapshot.Version)
	}
}

func TestSnapshotBeforeParse(t *testing.T) {
	t.Parallel()

	initial := refreshConfig{}
	plugin := &refreshPlugin{refresh: func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error) {
		return plugins.RefreshOutcome{}, nil
	}}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}

	var snapshot refreshConfig
	if err := manager.Snapshot(&snapshot); !errors.Is(err, xconfig.ErrNotParsed) {
		t.Fatalf("Snapshot() error = %v, want ErrNotParsed", err)
	}
	if result := manager.Refresh(t.Context()); !errors.Is(result.Err, xconfig.ErrNotParsed) {
		t.Fatalf("Refresh() error = %v, want ErrNotParsed", result.Err)
	}
}

func TestParsePreservesCallerAndExplicitSharedDependencies(t *testing.T) {
	t.Parallel()

	all := []int{1, 2, 3}
	client := &http.Client{}
	extra := map[string]string{"key": "value"}
	initial := struct {
		All    []int
		Head   []int
		Client *http.Client `xconfig_shared:"true"`
		Out    *os.File     `xconfig_shared:"true"`
		Extra  map[string]string
	}{All: all, Head: all[:1], Client: client, Out: os.Stdout, Extra: extra}

	manager, err := xconfig.Custom(&initial)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(initial.Head) != 1 {
		t.Fatalf("Parse() Head length = %d, want 1", len(initial.Head))
	}
	if initial.Client != client || initial.Out != os.Stdout {
		t.Fatal("Parse() replaced opaque dependency pointers")
	}
	extra["shared"] = "yes"
	if initial.Extra["shared"] != "yes" {
		t.Fatal("Parse() replaced caller-owned map")
	}

	snapshot, err := xconfig.Snapshot[struct {
		All    []int
		Head   []int
		Client *http.Client `xconfig_shared:"true"`
		Out    *os.File     `xconfig_shared:"true"`
		Extra  map[string]string
	}](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Head) != 1 || snapshot.Client != client || snapshot.Out != os.Stdout {
		t.Fatalf("Snapshot() corrupted aliases or opaque pointers: %+v", snapshot)
	}
	snapshot.Head[0] = 99
	if snapshot.All[0] == 99 {
		t.Fatal("Snapshot() retained an unnecessary slice backing-array alias")
	}
}

func TestSnapshotDeepClonesExternalPointersAndUnexportedState(t *testing.T) {
	t.Parallel()

	initial := refreshConfig{
		Endpoint: &url.URL{Scheme: "https", Host: "initial.example"},
		private:  map[string]int{"version": 1},
	}
	plugin := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		config.Endpoint.Host = "refreshed.example"
		config.private["version"] = 2
		return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Endpoint"}}}, nil
	}}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issued, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if result := manager.Refresh(t.Context()); result.Err != nil {
		t.Fatalf("Refresh() error = %v", result.Err)
	}
	latest, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if initial.Endpoint.Host != "initial.example" || initial.private["version"] != 1 {
		t.Fatalf("Refresh() mutated caller state: %+v", initial)
	}
	if issued.Endpoint.Host != "initial.example" || issued.private["version"] != 1 {
		t.Fatalf("Refresh() mutated an issued snapshot: %+v", issued)
	}
	if latest.Endpoint.Host != "refreshed.example" || latest.private["version"] != 2 {
		t.Fatalf("latest snapshot = %+v, want refreshed state", latest)
	}
	if latest.Endpoint == initial.Endpoint || latest.Endpoint == issued.Endpoint {
		t.Fatal("external config pointer was treated as an opaque dependency")
	}
}

func TestSnapshotCopiesSliceLengthNotCapacity(t *testing.T) {
	t.Parallel()

	data := make([]byte, 8, 1<<20)
	config := struct{ Data []byte }{Data: data}
	manager, err := xconfig.Custom(&config)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	snapshot, err := xconfig.Snapshot[struct{ Data []byte }](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if cap(snapshot.Data) != len(snapshot.Data) {
		t.Fatalf("Snapshot() copied cap=%d, want len=%d", cap(snapshot.Data), len(snapshot.Data))
	}
}

func TestSnapshotClonesUnexportedFieldsFromMapAndInterface(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 6, 12, 30, 0, 123, time.FixedZone("test", 3*60*60))
	config := struct {
		Times map[string]time.Time
		Any   any
	}{Times: map[string]time.Time{"a": now}, Any: now}
	manager, err := xconfig.Custom(&config)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	snapshot, err := xconfig.Snapshot[struct {
		Times map[string]time.Time
		Any   any
	}](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Times["a"] != now {
		t.Fatalf("map time = %v, want %v", snapshot.Times["a"], now)
	}
	gotAny, ok := snapshot.Any.(time.Time)
	if !ok || gotAny != now {
		t.Fatalf("interface time = %#v, want %v", snapshot.Any, now)
	}
}

func TestSnapshotRejectsNilConfig(t *testing.T) {
	t.Parallel()

	if _, err := xconfig.Snapshot[refreshConfig](nil); !errors.Is(err, xconfig.ErrNilConfig) {
		t.Fatalf("Snapshot(nil) error = %v, want ErrNilConfig", err)
	}
}

func TestSnapshotDoesNotWaitForRefreshPlugin(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	plugin := &refreshPlugin{refresh: func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error) {
		close(entered)
		<-release
		return plugins.RefreshOutcome{}, nil
	}}
	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	refreshDone := make(chan struct{})
	go func() {
		manager.Refresh(t.Context())
		close(refreshDone)
	}()
	<-entered
	snapshotDone := make(chan error, 1)
	go func() {
		_, snapshotErr := xconfig.Snapshot[refreshConfig](manager)
		snapshotDone <- snapshotErr
	}()
	select {
	case snapshotErr := <-snapshotDone:
		if snapshotErr != nil {
			t.Errorf("Snapshot() error = %v", snapshotErr)
		}
	case <-time.After(time.Second):
		close(release)
		<-refreshDone
		t.Fatal("Snapshot() waited for refresh plugin I/O")
	}
	close(release)
	<-refreshDone
}

func TestRefreshPluginCanReadSnapshot(t *testing.T) {
	t.Parallel()

	var manager xconfig.Config
	plugin := &refreshPlugin{refresh: func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error) {
		_, err := xconfig.Snapshot[refreshConfig](manager)
		return plugins.RefreshOutcome{}, err
	}}
	initial := refreshConfig{}
	var err error
	manager, err = xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	done := make(chan xconfig.RefreshResult, 1)
	go func() { done <- manager.Refresh(t.Context()) }()
	select {
	case result := <-done:
		if result.Err != nil {
			t.Fatalf("Refresh() error = %v", result.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("Refresh() deadlocked when plugin called Snapshot()")
	}
}

func TestIgnoredRefreshResultsDoNotStopLoop(t *testing.T) {
	t.Parallel()

	reached := make(chan struct{})
	var calls atomic.Int64
	plugin := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		config.Version++
		if calls.Add(1) == 32 {
			close(reached)
		}
		return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Version"}}}, nil
	}}
	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	results, err := manager.StartRefresh(t.Context(), time.Millisecond)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	t.Cleanup(manager.StopRefresh)
	select {
	case <-reached:
	case <-time.After(time.Second):
		t.Fatalf("refresh loop stopped after %d calls because result channel was ignored", calls.Load())
	}
	manager.StopRefresh()
	var reportedDrop bool
	for result := range results {
		reportedDrop = reportedDrop || result.Dropped > 0
	}
	if !reportedDrop {
		t.Fatal("coalesced refresh notifications did not report Dropped")
	}
}

func TestRefreshPublishesChangesAlongsideWarnings(t *testing.T) {
	t.Parallel()

	warning := errors.New("invalid optional value")
	plugin := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		config.Version = 2
		return plugins.RefreshOutcome{
			Changes:  []plugins.FieldChange{{FieldName: "Version"}},
			Warnings: []error{warning},
		}, nil
	}}
	initial := refreshConfig{Version: 1}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	result := manager.Refresh(t.Context())
	if result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want published result", result)
	}
	if len(result.Warnings) != 1 || !errors.Is(result.Warnings[0], warning) {
		t.Fatalf("warnings = %+v, want %v", result.Warnings, warning)
	}
	snapshot, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("Snapshot() Version = %d, want 2", snapshot.Version)
	}
}

func TestStartRefreshRejectsInvalidInterval(t *testing.T) {
	t.Parallel()

	ticks := make(chan struct{}, 2)
	initial := refreshConfig{}
	plugin := &refreshPlugin{refresh: func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error) {
		select {
		case ticks <- struct{}{}:
		default:
		}
		return plugins.RefreshOutcome{}, nil
	}}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := manager.StartRefresh(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("valid StartRefresh() error = %v", err)
	}
	t.Cleanup(manager.StopRefresh)
	select {
	case <-ticks:
	case <-time.After(time.Second):
		t.Fatal("valid refresh loop did not start")
	}
	if _, err := manager.StartRefresh(t.Context(), 0); !errors.Is(err, xconfig.ErrInvalidRefreshInterval) {
		t.Fatalf("StartRefresh() error = %v, want ErrInvalidRefreshInterval", err)
	}
	select {
	case <-ticks:
	case <-time.After(time.Second):
		t.Fatal("invalid StartRefresh() stopped the existing loop")
	}
}

func TestFailedRefreshDoesNotPublishPhantomChanges(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("later plugin failed")
	firstCall := true
	change := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		if !firstCall {
			return plugins.RefreshOutcome{}, nil
		}
		firstCall = false
		config.Version = 2
		return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Password"}}}, nil
	}}
	fail := true
	failure := &refreshPlugin{refresh: func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error) {
		if fail {
			fail = false
			return plugins.RefreshOutcome{}, sentinel
		}
		return plugins.RefreshOutcome{}, nil
	}}
	initial := refreshConfig{Version: 1}
	manager, err := xconfig.Custom(&initial, change, failure)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result := manager.Refresh(t.Context()); !errors.Is(result.Err, sentinel) {
		t.Fatalf("first Refresh() error = %v, want %v", result.Err, sentinel)
	}
	if result := manager.Refresh(t.Context()); result.Err != nil || result.Published || len(result.Changes) != 0 {
		t.Fatalf("second Refresh() = %+v, want no-op without phantom changes", result)
	}
}

func TestRefreshResultCoalescingPreservesErrorAndWarnings(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("vault unavailable")
	warning := errors.New("invalid optional secret")
	var calls atomic.Int64
	reached := make(chan struct{})
	plugin := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		call := calls.Add(1)
		if call == 1 {
			return plugins.RefreshOutcome{Warnings: []error{warning}}, sentinel
		}
		config.Version++
		if call == 20 {
			close(reached)
		}
		return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Version"}}}, nil
	}}
	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	results, err := manager.StartRefresh(t.Context(), time.Millisecond)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	select {
	case <-reached:
	case <-time.After(time.Second):
		manager.StopRefresh()
		t.Fatalf("refresh loop made %d calls, want at least 20", calls.Load())
	}
	manager.StopRefresh()

	var foundError, foundWarning bool
	for result := range results {
		foundError = foundError || errors.Is(result.Err, sentinel)
		for _, got := range result.Warnings {
			foundWarning = foundWarning || errors.Is(got, warning)
		}
	}
	if !foundError || !foundWarning {
		t.Fatalf("coalesced results lost error or warning: error=%v warning=%v", foundError, foundWarning)
	}
}

func TestRefreshResultCoalescingBoundsWarnings(t *testing.T) {
	t.Parallel()

	const callsWanted = 100
	warning := errors.New("invalid optional secret")
	var calls atomic.Int64
	reached := make(chan struct{})
	plugin := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		call := calls.Add(1)
		config.Version++
		if call == callsWanted {
			close(reached)
		}
		return plugins.RefreshOutcome{
			Changes:  []plugins.FieldChange{{FieldName: "Version"}},
			Warnings: []error{warning},
		}, nil
	}}
	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	results, err := manager.StartRefresh(t.Context(), 100*time.Microsecond)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	select {
	case <-reached:
	case <-time.After(time.Second):
		manager.StopRefresh()
		t.Fatalf("refresh loop made %d calls, want %d", calls.Load(), callsWanted)
	}
	manager.StopRefresh()

	for result := range results {
		if len(result.Warnings) > 16 {
			t.Fatalf("coalesced result retained %d warnings, want at most 16", len(result.Warnings))
		}
	}
}

func TestStopRefreshDoesNotEmitContextCanceled(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	var once sync.Once
	plugin := &refreshPlugin{refresh: func(ctx context.Context, _ *refreshConfig) (plugins.RefreshOutcome, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return plugins.RefreshOutcome{}, ctx.Err()
	}}
	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	results, err := manager.StartRefresh(t.Context(), time.Nanosecond)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh plugin did not start")
	}
	manager.StopRefresh()
	for result := range results {
		if errors.Is(result.Err, context.Canceled) {
			t.Fatalf("graceful StopRefresh emitted error: %v", result.Err)
		}
	}
}

type internedConfig struct {
	Addr    netip.Addr
	AddrPtr *netip.Addr
	Prefix  netip.Prefix
	Addrs   []netip.Addr
	ByName  map[string]netip.Addr
	Loc     *time.Location
}

func TestSnapshotPreservesInternedValueIdentity(t *testing.T) {
	t.Parallel()

	addrPtr := netip.MustParseAddr("172.16.0.1")
	initial := internedConfig{
		Addr:    netip.MustParseAddr("192.168.1.10"),
		AddrPtr: &addrPtr,
		Prefix:  netip.MustParsePrefix("192.168.1.0/24"),
		Addrs:   []netip.Addr{netip.MustParseAddr("10.0.0.1")},
		ByName:  map[string]netip.Addr{"gateway": netip.MustParseAddr("10.0.0.2")},
		Loc:     time.UTC,
	}
	manager, err := xconfig.Custom(&initial)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	snapshot, err := xconfig.Snapshot[internedConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if snapshot.Addr != initial.Addr || !snapshot.Addr.Is4() {
		t.Fatalf("Snapshot() Addr = %v (is4=%v), want %v", snapshot.Addr, snapshot.Addr.Is4(), initial.Addr)
	}
	if *snapshot.AddrPtr != *initial.AddrPtr || !snapshot.AddrPtr.Is4() {
		t.Fatalf("Snapshot() *AddrPtr = %v (is4=%v), want %v", *snapshot.AddrPtr, snapshot.AddrPtr.Is4(), *initial.AddrPtr)
	}
	if snapshot.AddrPtr == initial.AddrPtr {
		t.Fatal("Snapshot() shared a config pointer instead of owning it")
	}
	if snapshot.Prefix != initial.Prefix {
		t.Fatalf("Snapshot() Prefix = %v, want %v", snapshot.Prefix, initial.Prefix)
	}
	if snapshot.Addrs[0] != initial.Addrs[0] || !snapshot.Addrs[0].Is4() {
		t.Fatalf("Snapshot() slice element = %v, want %v", snapshot.Addrs[0], initial.Addrs[0])
	}
	if snapshot.ByName["gateway"] != initial.ByName["gateway"] {
		t.Fatalf("Snapshot() map value = %v, want %v", snapshot.ByName["gateway"], initial.ByName["gateway"])
	}
	if snapshot.Loc != time.UTC {
		t.Fatalf("Snapshot() Loc = %v, want the time.UTC sentinel", snapshot.Loc)
	}
}

func TestSnapshotIsIndependentOfSnapshotOrder(t *testing.T) {
	t.Parallel()

	type valueConfig struct{ Value int }
	snapshotAfterMutation := func(t *testing.T, readFirst bool) int {
		t.Helper()
		initial := valueConfig{Value: 1}
		manager, err := xconfig.Custom(&initial)
		if err != nil {
			t.Fatalf("Custom() error = %v", err)
		}
		if err := manager.Parse(); err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if readFirst {
			if _, err := xconfig.Snapshot[valueConfig](manager); err != nil {
				t.Fatalf("first Snapshot() error = %v", err)
			}
		}
		initial.Value = 99
		snapshot, err := xconfig.Snapshot[valueConfig](manager)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		return snapshot.Value
	}

	withEarlyRead := snapshotAfterMutation(t, true)
	withoutEarlyRead := snapshotAfterMutation(t, false)
	if withEarlyRead != withoutEarlyRead {
		t.Fatalf("Snapshot() depends on call order: early read = %d, late read = %d", withEarlyRead, withoutEarlyRead)
	}
	if withEarlyRead != 1 {
		t.Fatalf("Snapshot() Value = %d, want the parsed value 1", withEarlyRead)
	}
}

type routeKeyTarget struct{ Host string }

type routeConfig struct {
	Routes map[[1]*routeKeyTarget]string
}

func TestSnapshotOwnsMapKeys(t *testing.T) {
	t.Parallel()

	key := [1]*routeKeyTarget{{Host: "original"}}
	initial := routeConfig{Routes: map[[1]*routeKeyTarget]string{key: "route"}}
	manager, err := xconfig.Custom(&initial)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	snapshot, err := xconfig.Snapshot[routeConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Routes) != 1 {
		t.Fatalf("Snapshot() Routes = %+v, want one entry", snapshot.Routes)
	}
	for snapshotKey, value := range snapshot.Routes {
		if value != "route" {
			t.Fatalf("Snapshot() route value = %q, want %q", value, "route")
		}
		snapshotKey[0].Host = "mutated"
	}
	if key[0].Host != "original" {
		t.Fatalf("Snapshot() aliased map key data: %q", key[0].Host)
	}
}

func TestRefreshPublishesNewContainerEntries(t *testing.T) {
	t.Parallel()

	initial := refreshConfig{Values: map[string]int{"existing": 1}}
	plugin := &refreshPlugin{refresh: func(_ context.Context, config *refreshConfig) (plugins.RefreshOutcome, error) {
		config.Values["added"] = 0
		return plugins.RefreshOutcome{}, nil
	}}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	result := manager.Refresh(t.Context())
	if result.Err != nil || !result.Published {
		t.Fatalf("Refresh() = %+v, want a published result", result)
	}
	snapshot, err := xconfig.Snapshot[refreshConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if _, ok := snapshot.Values["added"]; !ok {
		t.Fatalf("Snapshot() Values = %+v, want the new container entry", snapshot.Values)
	}

	if result := manager.Refresh(t.Context()); result.Err != nil || result.Published {
		t.Fatalf("second Refresh() = %+v, want an unpublished no-op", result)
	}
}

func TestUsageDoesNotWaitForRefreshPlugin(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	plugin := &refreshPlugin{refresh: func(context.Context, *refreshConfig) (plugins.RefreshOutcome, error) {
		close(entered)
		<-release
		return plugins.RefreshOutcome{}, nil
	}}
	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	refreshDone := make(chan struct{})
	go func() {
		manager.Refresh(t.Context())
		close(refreshDone)
	}()
	<-entered
	usageDone := make(chan error, 1)
	go func() {
		_, usageErr := manager.Usage()
		usageDone <- usageErr
	}()
	select {
	case usageErr := <-usageDone:
		if usageErr != nil {
			t.Errorf("Usage() error = %v", usageErr)
		}
	case <-time.After(time.Second):
		close(release)
		<-refreshDone
		t.Fatal("Usage() waited for refresh plugin I/O")
	}
	close(release)
	<-refreshDone
}

func TestParsePluginCanReadUsageAndSnapshot(t *testing.T) {
	t.Parallel()

	var manager xconfig.Config
	plugin := &parsePlugin{parse: func() error {
		if _, err := manager.Usage(); err != nil {
			return fmt.Errorf("Usage() error = %w", err)
		}
		if _, err := xconfig.Snapshot[refreshConfig](manager); !errors.Is(err, xconfig.ErrNotParsed) {
			return fmt.Errorf("Snapshot() error = %v, want ErrNotParsed", err)
		}
		return nil
	}}
	initial := refreshConfig{}
	var err error
	manager, err = xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- manager.Parse() }()
	select {
	case parseErr := <-done:
		if parseErr != nil {
			t.Fatalf("Parse() error = %v", parseErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Parse() deadlocked when the plugin called Usage() and Snapshot()")
	}
}

func TestStartRefreshRequiresRefreshablePlugin(t *testing.T) {
	t.Parallel()

	initial := refreshConfig{}
	manager, err := xconfig.Custom(&initial, &parsePlugin{})
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	t.Cleanup(manager.StopRefresh)

	results, err := manager.StartRefresh(t.Context(), time.Millisecond)
	if !errors.Is(err, xconfig.ErrNoRefreshablePlugins) {
		t.Fatalf("StartRefresh() error = %v, want ErrNoRefreshablePlugins", err)
	}
	if results != nil {
		t.Fatalf("StartRefresh() returned a channel for a config without refreshable plugins")
	}
}

type ownedOpaqueConfig struct {
	Amount *big.Int
	Buf    *bytes.Buffer
}

func TestSnapshotOwnsMutableOpaquePointers(t *testing.T) {
	t.Parallel()

	initial := ownedOpaqueConfig{Amount: big.NewInt(1), Buf: bytes.NewBufferString("initial")}
	manager, err := xconfig.Custom(&initial)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	snapshot, err := xconfig.Snapshot[ownedOpaqueConfig](manager)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Amount == initial.Amount || snapshot.Buf == initial.Buf {
		t.Fatal("Snapshot() shared a mutable opaque pointer instead of owning it")
	}

	// Both mutations reuse the existing backing storage, exactly like the
	// in-place update flat performs on a pointer TextUnmarshaler field.
	initial.Amount.SetInt64(999)
	initial.Buf.Reset()
	initial.Buf.WriteString("mutated")

	if got := snapshot.Amount.Int64(); got != 1 {
		t.Fatalf("Snapshot() Amount = %d, want 1", got)
	}
	if got := snapshot.Buf.String(); got != "initial" {
		t.Fatalf("Snapshot() Buf = %q, want %q", got, "initial")
	}
}

type hookConfig struct {
	Hook   func()
	Values map[string]int
}

func TestRefreshDoesNotRepublishUnchangedFuncField(t *testing.T) {
	t.Parallel()

	expand := false
	plugin := &noopRefreshPlugin{refresh: func(target any) {
		if expand {
			target.(*hookConfig).Values["added"] = 0
		}
	}}
	initial := hookConfig{Hook: func() {}, Values: map[string]int{"existing": 1}}
	manager, err := xconfig.Custom(&initial, plugin)
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	for i := range 3 {
		if result := manager.Refresh(t.Context()); result.Err != nil || result.Published {
			t.Fatalf("refresh %d = %+v, want an unpublished no-op", i, result)
		}
	}

	expand = true
	if result := manager.Refresh(t.Context()); result.Err != nil || !result.Published {
		t.Fatalf("expanding Refresh() = %+v, want a published result", result)
	}
}

func TestRefreshDoesNotRepublishClonedMapKeys(t *testing.T) {
	t.Parallel()

	initial := routeConfig{Routes: map[[1]*routeKeyTarget]string{{{Host: "original"}}: "route"}}
	manager, err := xconfig.Custom(&initial, &noopRefreshPlugin{})
	if err != nil {
		t.Fatalf("Custom() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	for i := range 3 {
		if result := manager.Refresh(t.Context()); result.Err != nil || result.Published {
			t.Fatalf("refresh %d = %+v, want an unpublished no-op", i, result)
		}
	}
}
