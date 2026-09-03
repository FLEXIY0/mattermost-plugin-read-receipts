package main

const (
	defaultTickSize = 11
	minTickSize     = 6
	maxTickSize     = 20
)

// configuration mirrors the settings_schema in plugin.json. Field names must
// match the setting keys exactly — LoadPluginConfiguration maps them by name.
type configuration struct {
	TickSize int

	// A pointer so an upgraded install, whose stored config predates this
	// setting, is told apart from an admin who deliberately switched it off.
	// Absent means on; a plain bool would silently default such installs to
	// false and quietly disable the feature on upgrade.
	ChannelReadSync *bool
}

// normalized pulls out-of-range values back to the default. The System Console
// number field does not enforce bounds, and an admin typing 0 or 500 must not
// be able to break the rendering of every post in the instance.
func (c configuration) normalized() configuration {
	if c.TickSize < minTickSize || c.TickSize > maxTickSize {
		c.TickSize = defaultTickSize
	}

	if c.ChannelReadSync == nil {
		enabled := true
		c.ChannelReadSync = &enabled
	}

	return c
}

func (c configuration) channelReadSyncEnabled() bool {
	return c.ChannelReadSync == nil || *c.ChannelReadSync
}

func (p *Plugin) OnConfigurationChange() error {
	// Called once before OnActivate, when there may be no API to load through.
	if p.API == nil {
		return nil
	}

	var cfg configuration
	if err := p.API.LoadPluginConfiguration(&cfg); err != nil {
		return err
	}

	normalized := cfg.normalized()
	p.config.Store(&normalized)

	return nil
}

func (p *Plugin) getConfiguration() configuration {
	if cfg := p.config.Load(); cfg != nil {
		return *cfg
	}

	return configuration{TickSize: defaultTickSize}
}
