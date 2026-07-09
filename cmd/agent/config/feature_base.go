//go:build !metax

package config

type FeatureConfig struct{}

func defaultFeatureConfig() FeatureConfig {
	return FeatureConfig{}
}

func (FeatureConfig) ApplyDefaults() {}

func (FeatureConfig) String() string {
	return ""
}
