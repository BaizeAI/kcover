//go:build metax

package config

import (
	"reflect"
	"testing"
)

func TestLoadParsesMetaXConfigForMetaxBuild(t *testing.T) {
	t.Parallel()

	cfg, err := Load(writeConfigFile(t, "interval: 9\nmetaX:\n  gpuNum: 16\n  temperature: 70\n"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	metaX := cfg.Features.MetaX
	if metaX.GPUNum != 16 {
		t.Fatalf("metaX.GPUNum = %d, want 16", metaX.GPUNum)
	}
	if metaX.Temperature != 70 {
		t.Fatalf("metaX.Temperature = %d, want 70", metaX.Temperature)
	}
	if metaX.Day2CheckTime != DefaultMetaXDay2CheckTime {
		t.Fatalf("metaX.Day2CheckTime = %q, want %q", metaX.Day2CheckTime, DefaultMetaXDay2CheckTime)
	}
}

func TestLoadParsesMetaXHCAIDs(t *testing.T) {
	t.Parallel()

	cfg, err := Load(writeConfigFile(t, "metaX:\n  hcaIDs:\n    - mlx5_0\n    - mlx5_4\n"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	metaX := cfg.Features.MetaX
	want := []string{"mlx5_0", "mlx5_4"}
	if !reflect.DeepEqual(metaX.HCAIDs, want) {
		t.Fatalf("metaX.HCAIDs = %v, want %v", metaX.HCAIDs, want)
	}
}

func TestLoadReturnsMetaXDefaultsWhenPathIsEmpty(t *testing.T) {
	t.Parallel()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	metaX := cfg.Features.MetaX
	if metaX.GPUNum != DefaultMetaXGPUNum {
		t.Fatalf("metaX.GPUNum = %d, want %d", metaX.GPUNum, DefaultMetaXGPUNum)
	}
}

func TestApplyDefaultsRepairsInvalidMetaXValues(t *testing.T) {
	t.Parallel()

	cfg := Agent{
		Features: FeatureConfig{
			MetaX: MetaX{
				GPUNum:             -1,
				Temperature:        0,
				ECCMaxCount:        -1,
				NTPMaxOffsetMillis: 0,
				Day2CheckTime:      "24:00",
			},
		},
	}

	cfg.ApplyDefaults()

	metaX := cfg.Features.MetaX
	if metaX.GPUNum != DefaultMetaXGPUNum {
		t.Fatalf("metaX.GPUNum = %d, want %d", metaX.GPUNum, DefaultMetaXGPUNum)
	}
	if metaX.Temperature != DefaultMetaXTemperature {
		t.Fatalf("metaX.Temperature = %d, want %d", metaX.Temperature, DefaultMetaXTemperature)
	}
	if metaX.ECCMaxCount != DefaultMetaXECCMaxCount {
		t.Fatalf("metaX.ECCMaxCount = %d, want %d", metaX.ECCMaxCount, DefaultMetaXECCMaxCount)
	}
	if metaX.NTPMaxOffsetMillis != DefaultMetaXNTPMaxOffsetMS {
		t.Fatalf("metaX.NTPMaxOffsetMillis = %v, want %v", metaX.NTPMaxOffsetMillis, DefaultMetaXNTPMaxOffsetMS)
	}
	if metaX.Day2CheckTime != DefaultMetaXDay2CheckTime {
		t.Fatalf("metaX.Day2CheckTime = %q, want %q", metaX.Day2CheckTime, DefaultMetaXDay2CheckTime)
	}
}
