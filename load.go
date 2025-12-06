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

	ps := make([]plugins.Plugin, 0)

	if !o.skipDefaults {
		ps = append(ps, defaults.New())
	}

	if !o.skipCustomDefaults {
		ps = append(ps, customdefaults.New())
	}

	if !o.skipFiles {
		ps = append(ps, o.loader.Plugins()...)
	}

	// Apply defaults again after loading files to fill in zero values in loaded structs
	if !o.skipDefaults {
		ps = append(ps, defaults.New())
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

	c, err := Custom(conf, ps...)
	if err != nil {
		return c, err
	}

	c.setOptions(o)

	if err := c.Parse(); err != nil {
		return c, err
	}

	return c, err
}
