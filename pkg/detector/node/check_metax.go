//go:build metax

package node

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

var gpuPrefix = []byte("GPU#")

const expectedGPUStatus = "Available"
const defaultExpectedHCAState = "PORT_ACTIVE"

func (d *metaXDetector) check() error {
	klog.InfoS("MetaX day2 check started", "check", "gpu availability", "node", d.config.NodeName, "requiredGPUCount", d.config.GPUNum)
	if err := gpuCheck(d.config.GPUNum); err != nil {
		logDay2CheckResult("gpu availability", err, "node", d.config.NodeName, "requiredGPUCount", d.config.GPUNum)
		return err
	}
	logDay2CheckResult("gpu availability", nil, "node", d.config.NodeName, "requiredGPUCount", d.config.GPUNum)

	klog.InfoS("MetaX day2 check started", "check", "temperature", "node", d.config.NodeName, "maxTemperatureC", d.config.Temperature)
	if err := temperatureCheck(d.config.Temperature); err != nil {
		logDay2CheckResult("temperature", err, "node", d.config.NodeName, "maxTemperatureC", d.config.Temperature)
		return err
	}
	logDay2CheckResult("temperature", nil, "node", d.config.NodeName, "maxTemperatureC", d.config.Temperature)

	// 因为是在容器中，所以不再执行 NTP 偏移检查。
	//	klog.InfoS("MetaX day2 check started", "check", "ntp sync", "node", d.config.NodeName, "maxOffsetMillis", d.config.NTPMaxOffsetMillis)
	//	if err := ntpSyncCheck(d.config.NTPMaxOffsetMillis); err != nil {
	//		logDay2CheckResult("ntp sync", err, "node", d.config.NodeName, "maxOffsetMillis", d.config.NTPMaxOffsetMillis)
	//		return err
	//	}
	//	logDay2CheckResult("ntp sync", nil, "node", d.config.NodeName, "maxOffsetMillis", d.config.NTPMaxOffsetMillis)

	klog.InfoS("MetaX day2 check started", "check", "ecc fault page", "node", d.config.NodeName, "maxECCCount", d.config.ECCMaxCount)
	if err := eccFaultPageCheck(d.config.ECCMaxCount); err != nil {
		logDay2CheckResult("ecc fault page", err, "node", d.config.NodeName, "maxECCCount", d.config.ECCMaxCount)
		return err
	}
	logDay2CheckResult("ecc fault page", nil, "node", d.config.NodeName, "maxECCCount", d.config.ECCMaxCount)

	klog.InfoS("MetaX day2 check started", "check", "hca state", "node", d.config.NodeName, "hcaIDs", d.config.HCAIDs)
	err := hcaStateCheck(d.config.HCAIDs)
	logDay2CheckResult("hca state", err, "node", d.config.NodeName, "hcaIDs", d.config.HCAIDs)
	return err
}

func logDay2CheckResult(name string, err error, kvs ...any) {
	fields := append([]any{"check", name}, kvs...)
	if err != nil {
		klog.InfoS("MetaX day2 check failed", append(fields, "error", err.Error())...)
		return
	}

	klog.InfoS("MetaX day2 check succeeded", fields...)
}

func runCmd(cmd string, args ...string) ([]byte, error) {
	c := exec.Command(cmd, args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	c.Stdout = &out
	c.Stderr = &stderr

	err := c.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to run command %s %v: %v, stderr: %s", cmd, args, err, stderr.String())
	}

	return out.Bytes(), nil
}

/*
mx-smi  version: 2.2.9
GPU#0    MXC500      0000:08:00.0   Available (UUID: GPU-e702f06b-bbd6-bc06-5196-90a5554a24ba)
GPU#7    MXC500      0000:3c:00.0   Available (UUID: GPU-0e715b45-bcef-1500-e2db-752724095700)
*/

func gpuCheck(requiredGPUCount int) error {
	data, err := runCmd("mx-smi", "-L")
	if err != nil {
		return err
	}
	statuses, err := parseGPUStatuses(data, gpuPrefix)
	if err != nil {
		return fmt.Errorf("parse GPU status from mx-smi -L: %w", err)
	}
	return gpuStatusCountCheck(requiredGPUCount, statuses, expectedGPUStatus)
}

func gpuStatusCountCheck(requiredCount int, statuses map[string]string, expectedStatus string) error {
	count := 0
	var offenders []string
	for gpu, status := range statuses {
		if status == expectedStatus {
			count++
			continue
		}
		offenders = append(offenders, fmt.Sprintf("%s=%s", gpu, status))
	}
	sort.Strings(offenders)

	if count >= requiredCount {
		return nil
	}

	if len(offenders) == 0 {
		return fmt.Errorf("insufficient available GPUs: expected %d, found %d", requiredCount, count)
	}

	return fmt.Errorf("insufficient available GPUs: expected %d, found %d: %s", requiredCount, count, strings.Join(offenders, ", "))
}

func parseGPUStatuses(text []byte, prefix []byte) (map[string]string, error) {
	statuses := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(text))

	for scanner.Scan() {
		line := scanner.Bytes()
		fields := bytes.Fields(line)

		if len(fields) < 4 || !bytes.HasPrefix(fields[0], prefix) {
			continue
		}
		statuses[string(fields[0])] = string(fields[3])
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return statuses, nil
}

// mx-smi --show-temperature | grep hotspot
func temperatureCheck(maxTemperature int) error {
	data, err := runCmd("mx-smi", "--show-temperature")
	if err != nil {
		return err
	}

	temperatures, err := parseHotspotTemperatures(data, []byte("hotspot("))
	if err != nil {
		return fmt.Errorf("parse hotspot temperature from mx-smi --show-temperature: %w", err)
	}

	offenders := hotspotTemperaturesExceedingLimit(temperatures, maxTemperature)
	if len(offenders) > 0 {
		return fmt.Errorf("hotspot temperature exceeds limit %dC: %s", maxTemperature, strings.Join(offenders, ", "))
	}
	return nil
}

func hotspotTemperaturesExceedingLimit(temperatures map[string]float64, limit int) []string {
	var offenders []string
	maxTemperature := float64(limit)
	for gpu, temperature := range temperatures {
		if temperature >= maxTemperature {
			offenders = append(offenders, fmt.Sprintf("%s=%.2fC", gpu, temperature))
		}
	}
	sort.Strings(offenders)
	return offenders
}

func parseHotspotTemperatures(text []byte, prefix []byte) (map[string]float64, error) {
	temperatures := make(map[string]float64)
	scanner := bufio.NewScanner(bytes.NewReader(text))
	curGPU := ""

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, gpuPrefix) {
			fields := bytes.Fields(line)
			if len(fields) == 0 {
				return nil, fmt.Errorf("invalid gpu line: %q", line)
			}
			curGPU = string(fields[0])
			continue
		}

		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		if curGPU == "" {
			return nil, fmt.Errorf("hotspot line before gpu header: %q", line)
		}

		parts := bytes.SplitN(line, []byte(":"), 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid hotspot line: %q", line)
		}

		text := bytes.TrimSpace(parts[1])
		text = bytes.TrimSuffix(text, []byte("C"))
		text = bytes.TrimSpace(text)

		t, err := strconv.ParseFloat(string(text), 64)
		if err != nil {
			return nil, fmt.Errorf("parse hotspot temperature from %q: %w", line, err)
		}

		temperatures[curGPU] = t
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return temperatures, nil
}

/*
mx-smi  version: 2.2.9

=================== MetaX System Management Interface Log ===================
Timestamp                                         : Thu Apr 23 08:02:53 2026

Attached GPUs                                     : 8
GPU#0  MXC500  0000:08:00.0
    ECC Errors
        SRAM Correctable                          : 0
    Retired Pages
        Double Bit ECC                            : 0
*/
// GPU ECC坏页检查: mx-smi --count-ecc | grep 'Double Bit ECC'
func eccFaultPageCheck(maxCount int) error {
	data, err := runCmd("mx-smi", "--count-ecc")
	if err != nil {
		return err
	}

	values, err := parseGPUIntegerMetric(data, []byte("Double Bit ECC"))
	if err != nil {
		return fmt.Errorf("parse double-bit ECC count from mx-smi --count-ecc: %w", err)
	}

	offenders := doubleBitECCExceedingLimit(values, maxCount)
	if len(offenders) > 0 {
		return fmt.Errorf("ECC fault detected: double-bit ECC count exceeds limit %d: %s", maxCount, strings.Join(offenders, ", "))
	}

	return nil
}

func doubleBitECCExceedingLimit(values map[string]int, limit int) []string {
	var offenders []string
	for gpu, value := range values {
		if value > limit {
			offenders = append(offenders, fmt.Sprintf("%s=%d", gpu, value))
		}
	}
	sort.Strings(offenders)
	return offenders
}

func parseGPUIntegerMetric(text []byte, prefix []byte) (map[string]int, error) {
	values := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(text))
	curGPU := ""

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, gpuPrefix) {
			fields := bytes.Fields(line)
			if len(fields) == 0 {
				return nil, fmt.Errorf("invalid gpu line: %q", line)
			}
			curGPU = string(fields[0])
			continue
		}

		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		if curGPU == "" {
			return nil, fmt.Errorf("metric line before gpu header: %q", line)
		}

		parts := bytes.SplitN(line, []byte(":"), 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid metric line: %q", line)
		}

		valueText := string(bytes.TrimSpace(parts[1]))
		value, err := strconv.Atoi(valueText)
		if err != nil {
			return nil, fmt.Errorf("parse metric value from %q: %w", line, err)
		}

		values[curGPU] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

/*
"id": "ntp_sync",
'name":"NTP时间同步检查',
"command": "chronyc tracking | grep 'System time' | awk'{print ($4<0?-$4:$4)*1000<10?1:0}'",
"threshold": 1,
"description":"检查系统时间偏差是否小于0.01秒(10毫秒)"
*/
func ntpSyncCheck(maxOffsetMillis int32) error {
	data, err := runCmd("chronyc", "tracking")
	if err != nil {
		return err
	}

	offsetMillis, err := parseChronySystemTimeMillis(data)
	if err != nil {
		return fmt.Errorf("parse NTP offset from chronyc tracking: %w", err)
	}

	if offsetMillis > float64(maxOffsetMillis) {
		return fmt.Errorf("system time offset exceeds limit %.2f ms: actual=%.2f ms", float64(maxOffsetMillis), offsetMillis)
	}

	return nil
}

func parseChronySystemTimeMillis(text []byte) (float64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(text))
	prefix := []byte("System time")

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, prefix) {
			continue
		}

		parts := bytes.SplitN(line, []byte(":"), 2)
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid system time line: %q", line)
		}

		fields := bytes.Fields(parts[1])
		if len(fields) < 1 {
			return 0, fmt.Errorf("missing system time value: %q", line)
		}

		seconds, err := strconv.ParseFloat(string(fields[0]), 64)
		if err != nil {
			return 0, fmt.Errorf("parse system time from %q: %w", line, err)
		}

		return math.Abs(seconds) * 1000, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	return 0, fmt.Errorf("system time line not found")
}

func hcaStateCheck(requiredHCAIDs []string) error {
	if len(requiredHCAIDs) == 0 {
		return nil
	}

	data, err := runCmd("ibv_devinfo")
	if err != nil {
		return fmt.Errorf("check HCA state with ibv_devinfo: %w", err)
	}

	states, err := parseHCAStates(data)
	if err != nil {
		return fmt.Errorf("parse HCA state from ibv_devinfo: %w", err)
	}

	offenders := hcaIDsWithoutState(requiredHCAIDs, states, defaultExpectedHCAState)
	if len(offenders) > 0 {
		return fmt.Errorf("HCA state check failed: expected state %s: %s", defaultExpectedHCAState, strings.Join(offenders, ", "))
	}

	return nil
}

func parseHCAStates(text []byte) (map[string]string, error) {
	states := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(text))
	currentHCAID := ""

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("hca_id:")) {
			parts := bytes.SplitN(line, []byte(":"), 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid hca_id line: %q", line)
			}
			currentHCAID = string(bytes.TrimSpace(parts[1]))
			continue
		}

		if !bytes.HasPrefix(line, []byte("state:")) {
			continue
		}
		if currentHCAID == "" {
			return nil, fmt.Errorf("state line before hca_id: %q", line)
		}

		parts := bytes.SplitN(line, []byte(":"), 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid state line: %q", line)
		}

		states[currentHCAID] = string(bytes.TrimSpace(parts[1]))
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return states, nil
}

func hcaIDsWithoutState(requiredHCAIDs []string, states map[string]string, expectedState string) []string {
	var offenders []string
	for _, hcaID := range requiredHCAIDs {
		state, ok := states[hcaID]
		if !ok {
			offenders = append(offenders, fmt.Sprintf("%s=missing", hcaID))
			continue
		}
		if !strings.HasPrefix(state, expectedState) {
			offenders = append(offenders, fmt.Sprintf("%s=%s", hcaID, state))
		}
	}
	sort.Strings(offenders)
	return offenders
}
