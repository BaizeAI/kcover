package agentconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	DefaultPath     = "/etc/kcover-agent/config.yaml"
	DefaultInterval = 5
)

type Agent struct {
	Interval int          `yaml:"interval"`
	Flavor   FlavorConfig `yaml:",inline"`
}

func DefaultAgent() Agent {
	return Agent{
		Interval: DefaultInterval,
		Flavor:   defaultFlavorConfig(),
	}
}

func Load(path string) (Agent, error) {
	if path == "" {
		cfg := DefaultAgent()
		cfg.ApplyDefaults()
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, fmt.Errorf("read config file %q: %w", path, err)
	}

	cfg := DefaultAgent()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Agent{}, fmt.Errorf("unmarshal config file %q: %w", path, err)
	}

	cfg.ApplyDefaults()
	return cfg, nil
}

func (cfg Agent) String() string {
	summary := cfg.Flavor.String()
	if summary == "" {
		return fmt.Sprintf("intervalSeconds=%d", cfg.Interval)
	}

	return fmt.Sprintf("intervalSeconds=%d %s", cfg.Interval, summary)
}

func (cfg *Agent) ApplyDefaults() {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	cfg.Flavor.ApplyDefaults()
}
