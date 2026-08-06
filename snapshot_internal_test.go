package xconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/sxwebdev/xconfig/plugins"
)

type internalRefreshPlugin struct{}

func (*internalRefreshPlugin) Walk(any) error { return nil }

func (*internalRefreshPlugin) Parse() error { return nil }

func (*internalRefreshPlugin) Refresh(context.Context, any) (plugins.RefreshOutcome, error) {
	return plugins.RefreshOutcome{Changes: []plugins.FieldChange{{FieldName: "Value"}}}, nil
}

func TestParsePublishesSnapshotWithoutRefreshablePlugins(t *testing.T) {
	t.Parallel()

	config := struct{ Value int }{Value: 1}
	manager, err := newConfig(&config)
	if err != nil {
		t.Fatalf("newConfig() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if manager.current == nil {
		t.Fatal("Parse() did not publish a snapshot without a Refreshable plugin")
	}
	config.Value = 99
	var snapshot struct{ Value int }
	if err := manager.Snapshot(&snapshot); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Value != 1 {
		t.Fatalf("Snapshot() = %+v, want the parsed value 1", snapshot)
	}
}

func TestRefreshCloneFailureDiscardsStaging(t *testing.T) {
	t.Parallel()

	config := struct{ Value int }{Value: 1}
	manager, err := newConfig(&config, &internalRefreshPlugin{})
	if err != nil {
		t.Fatalf("newConfig() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	manager.staging = 42
	result := manager.Refresh(t.Context())
	if result.Err == nil {
		t.Fatal("Refresh() error = nil, want invalid staging clone error")
	}
	if manager.staging != nil {
		t.Fatalf("failed publish retained staging = %#v", manager.staging)
	}
}

func TestStopRefreshReleasesStagingWithoutRunningLoop(t *testing.T) {
	t.Parallel()

	config := struct{ Value int }{Value: 1}
	manager, err := newConfig(&config, &internalRefreshPlugin{})
	if err != nil {
		t.Fatalf("newConfig() error = %v", err)
	}
	if err := manager.Parse(); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	manager.staging = &config
	manager.StopRefresh()
	if manager.staging != nil {
		t.Fatalf("StopRefresh() retained staging = %#v", manager.staging)
	}
}

func TestMergeRefreshErrorsKeepsBoundedFirstAndLatestCauses(t *testing.T) {
	t.Parallel()

	first := errors.New("first")
	latest := first
	merged := error(first)
	for i := 0; i < 1000; i++ {
		latest = errors.New("latest")
		merged = mergeRefreshErrors(merged, latest)
	}
	if !errors.Is(merged, first) || !errors.Is(merged, latest) {
		t.Fatalf("merged error lost boundary causes: %v", merged)
	}
	coalesced, ok := merged.(*coalescedRefreshError)
	if !ok {
		t.Fatalf("merged error type = %T, want *coalescedRefreshError", merged)
	}
	for _, cause := range coalesced.Unwrap() {
		if _, nested := cause.(*coalescedRefreshError); nested {
			t.Fatal("coalesced error retained an unbounded nested chain")
		}
	}
}
