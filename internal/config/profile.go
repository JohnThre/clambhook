package config

// ProfileNames returns the names of all configured profiles.
func (c *Config) ProfileNames() []string {
	names := make([]string, len(c.Profiles))
	for i, p := range c.Profiles {
		names[i] = p.Name
	}
	return names
}

// ProfileByName returns a profile by its name.
func (c *Config) ProfileByName(name string) (*Profile, bool) {
	for i := range c.Profiles {
		if c.Profiles[i].Name == name {
			return &c.Profiles[i], true
		}
	}
	return nil, false
}
