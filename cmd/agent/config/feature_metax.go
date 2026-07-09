//go:build metax

package config

import (
	"fmt"
	"time"
)

const (
	DefaultMetaXDay2CheckTime  = "10:00"
	DefaultMetaXGPUNum         = 8
	DefaultMetaXTemperature    = 85
	DefaultMetaXECCMaxCount    = 64
	DefaultMetaXNTPMaxOffsetMS = 10
)

type MetaX struct {
	NodeName           string   `yaml:"-"`
	HCAIDs             []string `yaml:"hcaIDs"`
	GPUNum             int      `yaml:"gpuNum"`
	Temperature        int      `yaml:"temperature"`
	ECCMaxCount        int      `yaml:"eccMaxCount"`
	NTPMaxOffsetMillis int32    `yaml:"ntpMaxOffsetMillis"`
	Day2CheckTime      string   `yaml:"day2CheckTime"`
}

type FeatureConfig struct {
	MetaX MetaX `yaml:"metaX"`
}

func defaultFeatureConfig() FeatureConfig {
	return FeatureConfig{
		MetaX: defaultMetaXConfig(),
	}
}

func (cfg *FeatureConfig) ApplyDefaults() {
	if !validMetaXDay2CheckTime(cfg.MetaX.Day2CheckTime) {
		cfg.MetaX.Day2CheckTime = DefaultMetaXDay2CheckTime
	}
	if cfg.MetaX.NTPMaxOffsetMillis <= 0 {
		cfg.MetaX.NTPMaxOffsetMillis = DefaultMetaXNTPMaxOffsetMS
	}
	if cfg.MetaX.GPUNum < 0 {
		cfg.MetaX.GPUNum = DefaultMetaXGPUNum
	}
	if cfg.MetaX.Temperature <= 0 {
		cfg.MetaX.Temperature = DefaultMetaXTemperature
	}
	if cfg.MetaX.ECCMaxCount < 0 {
		cfg.MetaX.ECCMaxCount = DefaultMetaXECCMaxCount
	}
}

func (cfg FeatureConfig) String() string {
	return fmt.Sprintf(
		"metaX.day2CheckTime=%s metaX.gpuNum=%d metaX.temperature=%d metaX.eccMaxCount=%d metaX.ntpMaxOffsetMillis=%d metaX.hcaIDs=%v",
		cfg.MetaX.Day2CheckTime,
		cfg.MetaX.GPUNum,
		cfg.MetaX.Temperature,
		cfg.MetaX.ECCMaxCount,
		cfg.MetaX.NTPMaxOffsetMillis,
		cfg.MetaX.HCAIDs,
	)
}

func defaultMetaXConfig() MetaX {
	return MetaX{
		GPUNum:             DefaultMetaXGPUNum,
		Temperature:        DefaultMetaXTemperature,
		ECCMaxCount:        DefaultMetaXECCMaxCount,
		NTPMaxOffsetMillis: DefaultMetaXNTPMaxOffsetMS,
		Day2CheckTime:      DefaultMetaXDay2CheckTime,
	}
}

func validMetaXDay2CheckTime(value string) bool {
	if value == "" {
		return false
	}

	_, err := time.Parse("15:04", value)
	return err == nil
}
