//go:build !metax

package config

type FlavorConfig struct{}

func defaultFlavorConfig() FlavorConfig {
	return FlavorConfig{}
}

func (FlavorConfig) ApplyDefaults() {}

func (FlavorConfig) String() string {
	return ""
}
