//go:build !metax

package agentconfig

type FlavorConfig struct{}

func defaultFlavorConfig() FlavorConfig {
	return FlavorConfig{}
}

func (FlavorConfig) ApplyDefaults() {}

func (FlavorConfig) String() string {
	return ""
}
