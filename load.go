package xconfig

import (
	"github.com/sxwebdev/xconfig/plugins"
	"github.com/sxwebdev/xconfig/plugins/customdefaults"
	"github.com/sxwebdev/xconfig/plugins/defaults"
	"github.com/sxwebdev/xconfig/plugins/env"
	"github.com/sxwebdev/xconfig/plugins/flag"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

// Load creates a xconfig manager with defaults, environment variables,
// and flags (in that order) and optionally file loaders based on the provided
// Files map and parses them right away.
func Load(conf any, opts ...Option) (Config, error) {
	return load(conf, opts...)
}

func load(conf any, opts ...Option) (*config, error) {
	return loadConfig(conf, true, opts...)
}

func loadForDocumentation(conf any, opts ...Option) (*config, error) {
	return loadConfig(conf, false, opts...)
}

func loadConfig(conf any, publishSnapshot bool, opts ...Option) (*config, error) {
	o := &options{
		loader: &loader.Loader{},
	}
	for _, opt := range opts {
		opt(o)
	}

	// Apply disallow unknown fields to loader if set
	if o.loader != nil && o.disallowUnknownFields {
		o.loader.DisallowUnknownFields(true)
	}

	var watch *fileWatch
	if o.watchFiles && !o.skipFiles && o.loader != nil {
		var err error
		if watch, err = newFileWatch(conf, o); err != nil {
			return &config{target: conf, options: o}, err
		}
	}

	var filePlugins []plugins.Plugin
	switch {
	case watch != nil:
		filePlugins, _ = watch.applied.Plugins()
	case !o.skipFiles:
		filePlugins = o.loader.Plugins()
	}

	ps := o.chain(filePlugins)
	c, err := newConfig(conf, ps...)
	if err != nil {
		return c, err
	}

	c.options = o
	c.watch = watch

	err = c.parse(publishSnapshot)
	if watch != nil {
		c.acceptUnknownFields()
	}
	if err != nil {
		return c, err
	}

	return c, nil
}

// chain returns the plugins Load parses, lowest priority first, with files as
// the config file plugins.
func (o *options) chain(files []plugins.Plugin) []plugins.Plugin {
	ps := make([]plugins.Plugin, 0)

	// Register default metadata early for usage/documentation
	if !o.skipDefaults {
		ps = append(ps, defaults.NewMetaOnly())
	}

	if !o.skipCustomDefaults {
		ps = append(ps, customdefaults.New())
	}

	ps = append(ps, files...)

	// Apply defaults after loading files to fill in zero values including those in loaded maps
	// Use NewWithRescan to rescan the structure and find fields in maps that were created during loading
	if !o.skipDefaults {
		ps = append(ps, defaults.NewWithRescan(o.loader))
	}

	if !o.skipEnv {
		ps = append(ps, env.New(o.envPrefix))
	}

	if !o.skipFlags {
		ps = append(ps, flag.Standard())
	}

	if len(o.plugins) > 0 {
		ps = append(ps, o.plugins...)
	}

	return ps
}
