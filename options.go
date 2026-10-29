package xconfig

import (
	"github.com/sxwebdev/xconfig/plugins"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

type Option func(*options)

type options struct {
	// SkipDefaults set to true will not load config from 'default' tag.
	skipDefaults bool
	// SkipCustomDefaults set to true will not load config from 'SetDefault' method.
	skipCustomDefaults bool
	// SkipFiles set to true will not load config from files.
	skipFiles bool
	// SkipEnv set to true will not load config from environment variables.
	skipEnv bool
	// SkipFlags set to true will not load config from flag parameters.
	skipFlags bool

	// EnvPrefix is the prefix for environment variables.
	envPrefix string

	// DisallowUnknownFields set to true will cause loading to fail if unknown fields are found in config files.
	disallowUnknownFields bool

	// WatchFiles set to true will reload config files on refresh when their contents change.
	watchFiles  bool
	fileWatcher FileWatcher

	loader  *loader.Loader
	plugins []plugins.Plugin
}

func WithSkipDefaults() Option {
	return func(o *options) {
		o.skipDefaults = true
	}
}

func WithSkipCustomDefaults() Option {
	return func(o *options) {
		o.skipCustomDefaults = true
	}
}

func WithSkipFiles() Option {
	return func(o *options) {
		o.skipFiles = true
	}
}

func WithSkipEnv() Option {
	return func(o *options) {
		o.skipEnv = true
	}
}

func WithSkipFlags() Option {
	return func(o *options) {
		o.skipFlags = true
	}
}

func WithEnvPrefix(prefix string) Option {
	return func(o *options) {
		o.envPrefix = prefix
	}
}

func WithLoader(loader *loader.Loader) Option {
	return func(o *options) {
		o.loader = loader
	}
}

func WithPlugins(plugins ...plugins.Plugin) Option {
	return func(o *options) {
		o.plugins = append(o.plugins, plugins...)
	}
}

func WithDisallowUnknownFields() Option {
	return func(o *options) {
		o.disallowUnknownFields = true
	}
}

// WithWatchFiles makes Refresh, and so StartRefresh on every tick, reload the
// configuration when the contents of the loader's files change. A reload runs
// the whole chain again on the value passed to Load as it was before parsing,
// so env, flags, and plugins keep their priority over files and a key removed
// from a file falls back to its default. Plugins passed with WithPlugins are
// walked and visited again for every reload, except plugins.Refreshable ones,
// which are refreshed instead. A failed reload keeps the last published
// snapshot and is retried on the next refresh.
func WithWatchFiles() Option {
	return func(o *options) {
		o.watchFiles = true
	}
}

// WithFileWatcher enables WithWatchFiles and lets w wake the StartRefresh loop
// as soon as a file may have changed, instead of waiting for the next tick.
// Ticks keep checking the files, so events w misses are still picked up.
func WithFileWatcher(w FileWatcher) Option {
	return func(o *options) {
		o.watchFiles = true
		o.fileWatcher = w
	}
}
