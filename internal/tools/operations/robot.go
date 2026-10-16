package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/toolcontract"
)

const (
	defaultRobotWatchdogMS      = 5000
	defaultRobotMaxCommandMS    = 5000
	defaultRobotMaxLinearMPS    = 0.40
	defaultRobotMaxAngularRPS   = 0.80
	defaultRobotMaxDistanceM    = 1.00
	defaultRobotMaxTurnDegrees  = 180.0
	maxRobotCommandHistory      = 128
	maxRobotCommandIDLength     = 128
	maxRobotStopReasonLength    = 200
	robotMinimumCommandDuration = 10 * time.Millisecond
)

var robotCommandIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// RobotToolConfig controls the high-level robot runtime. Motion requires both
// the global and per-device allow_motion gates. The only driver implemented in
// this phase is sim, so enabling this config cannot silently access hardware.
type RobotToolConfig struct {
	Enabled       bool                         `json:"enabled" yaml:"enabled"`
	AllowMotion   bool                         `json:"allow_motion" yaml:"allow_motion"`
	DefaultDevice string                       `json:"default_device,omitempty" yaml:"default_device,omitempty"`
	Devices       map[string]RobotDeviceConfig `json:"devices,omitempty" yaml:"devices,omitempty"`
}

// RobotDeviceConfig defines the deterministic safety envelope for one robot.
// Future ROS2/serial/CAN drivers must implement the same constrained commands
// instead of exposing arbitrary topics, frames, registers, or shell commands.
type RobotDeviceConfig struct {
	Enabled        bool    `json:"enabled" yaml:"enabled"`
	Driver         string  `json:"driver" yaml:"driver"`
	AllowMotion    bool    `json:"allow_motion" yaml:"allow_motion"`
	WatchdogMS     int     `json:"watchdog_ms,omitempty" yaml:"watchdog_ms,omitempty"`
	MaxCommandMS   int     `json:"max_command_ms,omitempty" yaml:"max_command_ms,omitempty"`
	MaxLinearMPS   float64 `json:"max_linear_mps,omitempty" yaml:"max_linear_mps,omitempty"`
	MaxAngularRPS  float64 `json:"max_angular_rps,omitempty" yaml:"max_angular_rps,omitempty"`
	MaxDistanceM   float64 `json:"max_distance_m,omitempty" yaml:"max_distance_m,omitempty"`
	MaxTurnDegrees float64 `json:"max_turn_degrees,omitempty" yaml:"max_turn_degrees,omitempty"`
}

func defaultRobotToolConfig() RobotToolConfig {
	return RobotToolConfig{
		Enabled:       false,
		AllowMotion:   false,
		DefaultDevice: "sim_rover",
		Devices: map[string]RobotDeviceConfig{
			"sim_rover": {
				Enabled:        true,
				Driver:         "sim",
				AllowMotion:    false,
				WatchdogMS:     defaultRobotWatchdogMS,
				MaxCommandMS:   defaultRobotMaxCommandMS,
				MaxLinearMPS:   defaultRobotMaxLinearMPS,
				MaxAngularRPS:  defaultRobotMaxAngularRPS,
				MaxDistanceM:   defaultRobotMaxDistanceM,
				MaxTurnDegrees: defaultRobotMaxTurnDegrees,
			},
		},
	}
}

func DefaultRobotToolConfig() RobotToolConfig { return defaultRobotToolConfig() }

func normalizeRobotToolConfig(config RobotToolConfig) (RobotToolConfig, error) {
	normalized := config
	normalized.DefaultDevice = strings.TrimSpace(config.DefaultDevice)
	normalized.Devices = make(map[string]RobotDeviceConfig, len(config.Devices))

	names := make([]string, 0, len(config.Devices))
	for rawName := range config.Devices {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return RobotToolConfig{}, fmt.Errorf("robot device name cannot be empty")
		}
		if name != rawName {
			return RobotToolConfig{}, fmt.Errorf("robot device name %q cannot have surrounding whitespace", rawName)
		}
		if !robotCommandIDPattern.MatchString(name) {
			return RobotToolConfig{}, fmt.Errorf("robot device %q must use letters, digits, dot, underscore, colon, or dash", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		device := config.Devices[name]
		device.Driver = strings.ToLower(strings.TrimSpace(device.Driver))
		if device.Driver == "" {
			device.Driver = "sim"
		}
		if device.Driver != "sim" {
			return RobotToolConfig{}, fmt.Errorf("robot device %q uses unsupported driver %q; this build currently supports sim only", name, device.Driver)
		}
		if device.WatchdogMS == 0 {
			device.WatchdogMS = defaultRobotWatchdogMS
		}
		if device.MaxCommandMS == 0 {
			device.MaxCommandMS = defaultRobotMaxCommandMS
		}
		if device.MaxLinearMPS == 0 {
			device.MaxLinearMPS = defaultRobotMaxLinearMPS
		}
		if device.MaxAngularRPS == 0 {
			device.MaxAngularRPS = defaultRobotMaxAngularRPS
		}
		if device.MaxDistanceM == 0 {
			device.MaxDistanceM = defaultRobotMaxDistanceM
		}
		if device.MaxTurnDegrees == 0 {
			device.MaxTurnDegrees = defaultRobotMaxTurnDegrees
		}
		if device.WatchdogMS < 100 || device.WatchdogMS > 60000 {
			return RobotToolConfig{}, fmt.Errorf("robot device %q watchdog_ms must be between 100 and 60000", name)
		}
		if device.MaxCommandMS < 100 || device.MaxCommandMS > 60000 {
			return RobotToolConfig{}, fmt.Errorf("robot device %q max_command_ms must be between 100 and 60000", name)
		}
		if device.MaxCommandMS > device.WatchdogMS {
			return RobotToolConfig{}, fmt.Errorf("robot device %q max_command_ms cannot exceed watchdog_ms", name)
		}
		if err := validateRobotPositiveLimit(name, "max_linear_mps", device.MaxLinearMPS, 5); err != nil {
			return RobotToolConfig{}, err
		}
		if err := validateRobotPositiveLimit(name, "max_angular_rps", device.MaxAngularRPS, 10); err != nil {
			return RobotToolConfig{}, err
		}
		if err := validateRobotPositiveLimit(name, "max_distance_m", device.MaxDistanceM, 100); err != nil {
			return RobotToolConfig{}, err
		}
		if err := validateRobotPositiveLimit(name, "max_turn_degrees", device.MaxTurnDegrees, 3600); err != nil {
			return RobotToolConfig{}, err
		}
		normalized.Devices[name] = device
	}

	if normalized.DefaultDevice == "" {
		for _, name := range names {
			if normalized.Devices[name].Enabled {
				normalized.DefaultDevice = name
				break
			}
		}
	} else {
		canonical := ""
		for _, name := range names {
			if strings.EqualFold(name, normalized.DefaultDevice) {
				canonical = name
				break
			}
		}
		if canonical == "" {
			return RobotToolConfig{}, fmt.Errorf("default robot device %q is not configured", normalized.DefaultDevice)
		}
		normalized.DefaultDevice = canonical
	}

	if normalized.Enabled {
		enabledCount := 0
		for _, device := range normalized.Devices {
			if device.Enabled {
				enabledCount++
			}
		}
		if enabledCount == 0 {
			return RobotToolConfig{}, fmt.Errorf("robot runtime is enabled but no robot device is enabled")
		}
		if normalized.DefaultDevice == "" || !normalized.Devices[normalized.DefaultDevice].Enabled {
			return RobotToolConfig{}, fmt.Errorf("default robot device must be enabled")
		}
	}
	return normalized, nil
}

func NormalizeRobotToolConfig(config RobotToolConfig) (RobotToolConfig, error) {
	return normalizeRobotToolConfig(config)
}

func validateRobotPositiveLimit(device, field string, value, hardMaximum float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > hardMaximum {
		return fmt.Errorf("robot device %q %s must be greater than 0 and no more than %s", device, field, strconv.FormatFloat(hardMaximum, 'f', -1, 64))
	}
	return nil
}

type robotDriverMotion struct {
	CommandID  string
	Kind       string
	DistanceM  float64
	AngleDeg   float64
	LinearMPS  float64
	AngularRPS float64
	Duration   time.Duration
}

type robotDriver interface {
	Name() string
	Status(context.Context) (bool, string, error)
	StartMotion(context.Context, robotDriverMotion) error
	Stop(context.Context) error
}

// simRobotDriver deliberately performs no I/O. Pose integration lives in the
// runtime so tests and future physical drivers share identical safety rules.
type simRobotDriver struct{}

func (d *simRobotDriver) Name() string { return "sim" }
func (d *simRobotDriver) Status(context.Context) (bool, string, error) {
	return true, "simulation driver ready", nil
}
func (d *simRobotDriver) StartMotion(context.Context, robotDriverMotion) error { return nil }
func (d *simRobotDriver) Stop(context.Context) error                           { return nil }

func newRobotDriver(config RobotDeviceConfig) (robotDriver, error) {
	switch config.Driver {
	case "sim":
		return &simRobotDriver{}, nil
	default:
		return nil, fmt.Errorf("unsupported robot driver %q", config.Driver)
	}
}

type robotActiveCommand struct {
	command      robotDriverMotion
	fingerprint  string
	startedAt    time.Time
	deadline     time.Time
	startX       float64
	startY       float64
	startHeading float64
	timer        *time.Timer
}

type robotStoredCommand struct {
	fingerprint string
	receipt     robotMoveReceipt
}

type robotDeviceRuntime struct {
	config        RobotDeviceConfig
	driver        robotDriver
	mode          string
	softwareStop  bool
	lastCommandID string
	lastOutcome   string
	x             float64
	y             float64
	headingDeg    float64
	linearMPS     float64
	angularRPS    float64
	active        *robotActiveCommand
	commands      map[string]robotStoredCommand
	commandOrder  []string
}

type RobotRuntimeHub struct {
	mu         sync.Mutex
	auditMu    sync.Mutex
	workspace  string
	auditPath  string
	config     RobotToolConfig
	configHash string
	configErr  error
	devices    map[string]*robotDeviceRuntime
}

var robotRuntimeHubs sync.Map

func newRobotRuntimeHub(workspace string, config RobotToolConfig) *RobotRuntimeHub {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	absolute, err := filepath.Abs(workspace)
	if err == nil {
		workspace = absolute
	}
	hub := &RobotRuntimeHub{
		workspace: workspace,
		auditPath: filepath.Join(workspace, ".openkitty", "robot", "audit.jsonl"),
		devices:   make(map[string]*robotDeviceRuntime),
	}
	hub.Configure(config)
	return hub
}

func NewRobotRuntimeHub(workspace string, config RobotToolConfig) *RobotRuntimeHub {
	return newRobotRuntimeHub(workspace, config)
}

func robotRuntimeHubFor(workspace string, config RobotToolConfig) *RobotRuntimeHub {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	key, err := filepath.Abs(workspace)
	if err != nil {
		key = filepath.Clean(workspace)
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	value, loaded := robotRuntimeHubs.LoadOrStore(key, newRobotRuntimeHub(workspace, config))
	hub := value.(*RobotRuntimeHub)
	if loaded {
		hub.Configure(config)
	}
	return hub
}

func RobotRuntimeHubFor(workspace string, config RobotToolConfig) *RobotRuntimeHub {
	return robotRuntimeHubFor(workspace, config)
}

// Configure applies a changed safety envelope atomically. Any active motion is
// stopped before devices are rebuilt; configuration reload can never preserve
// motion under a different set of limits.
func (h *RobotRuntimeHub) Configure(config RobotToolConfig) {
	normalized, normalizeErr := normalizeRobotToolConfig(config)
	hashInput := struct {
		Config RobotToolConfig `json:"config"`
		Error  string          `json:"error,omitempty"`
	}{Config: normalized}
	if normalizeErr != nil {
		hashInput.Config = config
		hashInput.Error = normalizeErr.Error()
	}
	encoded, _ := json.Marshal(hashInput)
	hash := string(encoded)

	h.mu.Lock()
	if h.configHash == hash {
		h.mu.Unlock()
		return
	}
	oldDevices := h.devices
	h.config = normalized
	if normalizeErr != nil {
		h.config = config
	}
	h.configErr = normalizeErr
	h.configHash = hash
	h.devices = make(map[string]*robotDeviceRuntime)
	if normalizeErr == nil {
		for name, deviceConfig := range normalized.Devices {
			driver, err := newRobotDriver(deviceConfig)
			if err != nil {
				h.configErr = err
				h.devices = make(map[string]*robotDeviceRuntime)
				break
			}
			h.devices[name] = &robotDeviceRuntime{
				config:   deviceConfig,
				driver:   driver,
				mode:     "idle",
				commands: make(map[string]robotStoredCommand),
			}
		}
	}
	h.mu.Unlock()

	for name, device := range oldDevices {
		if device.active != nil && device.active.timer != nil {
			device.active.timer.Stop()
		}
		_ = device.driver.Stop(context.Background())
		if device.active != nil {
			_ = h.appendAudit(robotAuditRecord{
				Timestamp: time.Now().UTC(),
				Device:    name,
				Driver:    device.driver.Name(),
				Action:    "stop",
				CommandID: device.active.command.CommandID,
				Outcome:   "configuration_reload",
				Reason:    "motion stopped because robot configuration changed",
			})
		}
	}
}

type robotAuditRecord struct {
	Timestamp time.Time              `json:"timestamp"`
	Device    string                 `json:"device"`
	Driver    string                 `json:"driver"`
	Action    string                 `json:"action"`
	CommandID string                 `json:"command_id,omitempty"`
	Outcome   string                 `json:"outcome"`
	Reason    string                 `json:"reason,omitempty"`
	Values    map[string]interface{} `json:"values,omitempty"`
}

func (h *RobotRuntimeHub) appendAudit(record robotAuditRecord) error {
	h.auditMu.Lock()
	defer h.auditMu.Unlock()
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(h.auditPath), 0750); err != nil {
		return fmt.Errorf("create robot audit directory: %w", err)
	}
	file, err := os.OpenFile(h.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open robot audit log: %w", err)
	}
	defer file.Close()
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode robot audit record: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write robot audit record: %w", err)
	}
	return nil
}

func (h *RobotRuntimeHub) resolveDeviceLocked(requested string) (string, *robotDeviceRuntime, error) {
	if h.configErr != nil {
		return "", nil, h.configErr
	}
	name := strings.TrimSpace(requested)
	if name == "" {
		name = h.config.DefaultDevice
	}
	device, ok := h.devices[name]
	if !ok {
		for configuredName, configuredDevice := range h.devices {
			if strings.EqualFold(configuredName, name) {
				name = configuredName
				device = configuredDevice
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", nil, fmt.Errorf("robot device %q is not configured", name)
	}
	if !device.config.Enabled {
		return "", nil, fmt.Errorf("robot device %q is disabled", name)
	}
	return name, device, nil
}

func (h *RobotRuntimeHub) integrateActiveLocked(device *robotDeviceRuntime, now time.Time) {
	active := device.active
	if active == nil || active.command.Duration <= 0 {
		return
	}
	fraction := float64(now.Sub(active.startedAt)) / float64(active.command.Duration)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	switch active.command.Kind {
	case "translate":
		distance := active.command.DistanceM * fraction
		headingRadians := active.startHeading * math.Pi / 180
		device.x = active.startX + distance*math.Cos(headingRadians)
		device.y = active.startY + distance*math.Sin(headingRadians)
	case "rotate":
		device.headingDeg = normalizeRobotHeading(active.startHeading + active.command.AngleDeg*fraction)
	}
}

func normalizeRobotHeading(value float64) float64 {
	for value > 180 {
		value -= 360
	}
	for value <= -180 {
		value += 360
	}
	return value
}

type robotMoveReceipt struct {
	OK               bool    `json:"ok"`
	Device           string  `json:"device"`
	Driver           string  `json:"driver"`
	CommandID        string  `json:"command_id"`
	Status           string  `json:"status"`
	Motion           string  `json:"motion"`
	DistanceM        float64 `json:"distance_m,omitempty"`
	AngleDeg         float64 `json:"angle_deg,omitempty"`
	LinearMPS        float64 `json:"linear_mps,omitempty"`
	AngularRPS       float64 `json:"angular_rps,omitempty"`
	DurationMS       int     `json:"duration_ms"`
	StartedAt        string  `json:"started_at"`
	Deadline         string  `json:"deadline"`
	IdempotentReplay bool    `json:"idempotent_replay"`
}

type RobotMoveReceipt = robotMoveReceipt

func (h *RobotRuntimeHub) Move(ctx context.Context, params map[string]interface{}) (robotMoveReceipt, error) {
	motion := strings.ToLower(strings.TrimSpace(configvalue.String(params, "motion", "")))
	if motion != "translate" && motion != "rotate" {
		return robotMoveReceipt{}, fmt.Errorf("motion must be translate or rotate")
	}
	commandID, err := normalizeRobotCommandID(configvalue.String(params, "command_id", ""))
	if err != nil {
		return robotMoveReceipt{}, err
	}

	h.mu.Lock()
	deviceName, device, err := h.resolveDeviceLocked(configvalue.String(params, "device", ""))
	if err != nil {
		h.mu.Unlock()
		return robotMoveReceipt{}, err
	}
	if !h.config.Enabled {
		h.mu.Unlock()
		return robotMoveReceipt{}, fmt.Errorf("robot runtime is disabled")
	}
	if !h.config.AllowMotion || !device.config.AllowMotion {
		h.mu.Unlock()
		return robotMoveReceipt{}, fmt.Errorf("motion is disabled for robot %q; enable both tools_config.robot.allow_motion and the device allow_motion gate", deviceName)
	}
	driverMotion, duration, fingerprint, err := buildRobotDriverMotion(commandID, motion, params, device.config)
	if err != nil {
		h.mu.Unlock()
		return robotMoveReceipt{}, err
	}
	if stored, exists := device.commands[commandID]; exists {
		if stored.fingerprint != fingerprint {
			h.mu.Unlock()
			return robotMoveReceipt{}, fmt.Errorf("command_id %q was already used with different robot motion parameters", commandID)
		}
		receipt := stored.receipt
		receipt.IdempotentReplay = true
		h.mu.Unlock()
		return receipt, nil
	}
	if device.active != nil {
		h.integrateActiveLocked(device, time.Now())
		h.mu.Unlock()
		return robotMoveReceipt{}, fmt.Errorf("robot %q is busy with command %s; call robot_stop before starting another motion", deviceName, device.active.command.CommandID)
	}

	now := time.Now()
	active := &robotActiveCommand{
		command:      driverMotion,
		fingerprint:  fingerprint,
		startedAt:    now,
		deadline:     now.Add(duration),
		startX:       device.x,
		startY:       device.y,
		startHeading: device.headingDeg,
	}
	device.active = active
	device.mode = "arming"
	device.softwareStop = false
	device.lastCommandID = commandID
	device.lastOutcome = "authorizing"
	driver := device.driver
	h.mu.Unlock()

	audit := robotAuditRecord{
		Timestamp: now.UTC(),
		Device:    deviceName,
		Driver:    driver.Name(),
		Action:    "move",
		CommandID: commandID,
		Outcome:   "authorized",
		Values: map[string]interface{}{
			"motion":      motion,
			"distance_m":  driverMotion.DistanceM,
			"angle_deg":   driverMotion.AngleDeg,
			"linear_mps":  driverMotion.LinearMPS,
			"angular_rps": driverMotion.AngularRPS,
			"duration_ms": int(duration.Milliseconds()),
		},
	}
	if err := h.appendAudit(audit); err != nil {
		h.clearArmingCommand(deviceName, commandID, "audit_failed")
		return robotMoveReceipt{}, fmt.Errorf("robot motion rejected because the audit record could not be persisted: %w", err)
	}
	if err := driver.StartMotion(ctx, driverMotion); err != nil {
		h.clearArmingCommand(deviceName, commandID, "driver_rejected")
		_ = h.appendAudit(robotAuditRecord{
			Device: deviceName, Driver: driver.Name(), Action: "move", CommandID: commandID,
			Outcome: "driver_rejected", Reason: err.Error(),
		})
		return robotMoveReceipt{}, fmt.Errorf("robot driver rejected command %s: %w", commandID, err)
	}

	startedAt := time.Now()
	h.mu.Lock()
	currentDevice := h.devices[deviceName]
	if currentDevice == nil || currentDevice.active != active {
		h.mu.Unlock()
		_ = driver.Stop(context.Background())
		return robotMoveReceipt{}, fmt.Errorf("robot command %s was canceled before motion started", commandID)
	}
	active.startedAt = startedAt
	active.deadline = startedAt.Add(duration)
	currentDevice.mode = "moving"
	currentDevice.lastOutcome = "moving"
	if motion == "translate" {
		direction := math.Copysign(1, driverMotion.DistanceM)
		currentDevice.linearMPS = direction * driverMotion.LinearMPS
		currentDevice.angularRPS = 0
	} else {
		direction := math.Copysign(1, driverMotion.AngleDeg)
		currentDevice.linearMPS = 0
		currentDevice.angularRPS = direction * driverMotion.AngularRPS
	}
	receipt := robotMoveReceipt{
		OK:         true,
		Device:     deviceName,
		Driver:     driver.Name(),
		CommandID:  commandID,
		Status:     "accepted",
		Motion:     motion,
		DistanceM:  driverMotion.DistanceM,
		AngleDeg:   driverMotion.AngleDeg,
		LinearMPS:  driverMotion.LinearMPS,
		AngularRPS: driverMotion.AngularRPS,
		DurationMS: int(duration.Milliseconds()),
		StartedAt:  startedAt.UTC().Format(time.RFC3339Nano),
		Deadline:   active.deadline.UTC().Format(time.RFC3339Nano),
	}
	storeRobotCommandLocked(currentDevice, commandID, fingerprint, receipt)
	active.timer = time.AfterFunc(duration, func() {
		h.finishCommand(deviceName, commandID, "completed")
	})
	h.mu.Unlock()

	_ = h.appendAudit(robotAuditRecord{
		Device: deviceName, Driver: driver.Name(), Action: "move", CommandID: commandID,
		Outcome: "started",
	})
	return receipt, nil
}

func buildRobotDriverMotion(commandID, motion string, params map[string]interface{}, config RobotDeviceConfig) (robotDriverMotion, time.Duration, string, error) {
	command := robotDriverMotion{CommandID: commandID, Kind: motion}
	var seconds float64
	switch motion {
	case "translate":
		if _, exists := params["angle_deg"]; exists {
			return command, 0, "", fmt.Errorf("angle_deg is not allowed for translate motion")
		}
		if _, exists := params["angular_rps"]; exists {
			return command, 0, "", fmt.Errorf("angular_rps is not allowed for translate motion")
		}
		distance, exists, err := robotNumberParam(params, "distance_m")
		if err != nil {
			return command, 0, "", err
		}
		if !exists || distance == 0 {
			return command, 0, "", fmt.Errorf("distance_m is required and cannot be zero for translate motion")
		}
		if math.Abs(distance) > config.MaxDistanceM {
			return command, 0, "", fmt.Errorf("distance_m %.4g exceeds the configured limit %.4g", distance, config.MaxDistanceM)
		}
		speed, exists, err := robotNumberParam(params, "linear_mps")
		if err != nil {
			return command, 0, "", err
		}
		if !exists {
			speed = math.Min(0.20, config.MaxLinearMPS)
		}
		if speed <= 0 || speed > config.MaxLinearMPS {
			return command, 0, "", fmt.Errorf("linear_mps must be greater than 0 and no more than %.4g", config.MaxLinearMPS)
		}
		command.DistanceM = distance
		command.LinearMPS = speed
		seconds = math.Abs(distance) / speed
	case "rotate":
		if _, exists := params["distance_m"]; exists {
			return command, 0, "", fmt.Errorf("distance_m is not allowed for rotate motion")
		}
		if _, exists := params["linear_mps"]; exists {
			return command, 0, "", fmt.Errorf("linear_mps is not allowed for rotate motion")
		}
		angle, exists, err := robotNumberParam(params, "angle_deg")
		if err != nil {
			return command, 0, "", err
		}
		if !exists || angle == 0 {
			return command, 0, "", fmt.Errorf("angle_deg is required and cannot be zero for rotate motion")
		}
		if math.Abs(angle) > config.MaxTurnDegrees {
			return command, 0, "", fmt.Errorf("angle_deg %.4g exceeds the configured limit %.4g", angle, config.MaxTurnDegrees)
		}
		speed, exists, err := robotNumberParam(params, "angular_rps")
		if err != nil {
			return command, 0, "", err
		}
		if !exists {
			speed = math.Min(0.50, config.MaxAngularRPS)
		}
		if speed <= 0 || speed > config.MaxAngularRPS {
			return command, 0, "", fmt.Errorf("angular_rps must be greater than 0 and no more than %.4g", config.MaxAngularRPS)
		}
		command.AngleDeg = angle
		command.AngularRPS = speed
		seconds = math.Abs(angle*math.Pi/180) / speed
	}

	duration := time.Duration(math.Ceil(seconds*1000)) * time.Millisecond
	if duration < robotMinimumCommandDuration {
		duration = robotMinimumCommandDuration
	}
	maxDuration := time.Duration(config.MaxCommandMS) * time.Millisecond
	watchdog := time.Duration(config.WatchdogMS) * time.Millisecond
	if watchdog < maxDuration {
		maxDuration = watchdog
	}
	if rawTimeout, exists := params["timeout_ms"]; exists && rawTimeout != nil {
		timeoutMS, err := robotIntegerParam(rawTimeout, 100, config.MaxCommandMS)
		if err != nil {
			return command, 0, "", fmt.Errorf("timeout_ms %w", err)
		}
		requested := time.Duration(timeoutMS) * time.Millisecond
		if requested < maxDuration {
			maxDuration = requested
		}
	}
	if duration > maxDuration {
		return command, 0, "", fmt.Errorf("motion needs %dms but the command/watchdog limit is %dms; use a shorter distance/angle or an allowed higher speed", duration.Milliseconds(), maxDuration.Milliseconds())
	}
	command.Duration = duration
	fingerprint := fmt.Sprintf("%s|%s|%.9f|%.9f|%.9f|%.9f|%d",
		commandID, motion, command.DistanceM, command.AngleDeg, command.LinearMPS, command.AngularRPS, duration.Milliseconds())
	return command, duration, fingerprint, nil
}

func robotNumberParam(params map[string]interface{}, key string) (float64, bool, error) {
	value, exists := params[key]
	if !exists || value == nil {
		return 0, false, nil
	}
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, true, fmt.Errorf("%s must be a number", key)
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, true, fmt.Errorf("%s must be a number", key)
		}
		number = parsed
	default:
		return 0, true, fmt.Errorf("%s must be a number", key)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, true, fmt.Errorf("%s must be finite", key)
	}
	return number, true, nil
}

func RobotNumberParam(params map[string]interface{}, key string) (float64, bool, error) {
	return robotNumberParam(params, key)
}

func robotIntegerParam(value interface{}, minimum, maximum int) (int, error) {
	number, _, err := robotNumberParam(map[string]interface{}{"value": value}, "value")
	if err != nil || math.Trunc(number) != number {
		return 0, fmt.Errorf("must be an integer")
	}
	if number < float64(minimum) || number > float64(maximum) {
		return 0, fmt.Errorf("must be between %d and %d", minimum, maximum)
	}
	parsed := int(number)
	return parsed, nil
}

func RobotIntegerParam(value interface{}, minimum, maximum int) (int, error) {
	return robotIntegerParam(value, minimum, maximum)
}

func normalizeRobotCommandID(raw string) (string, error) {
	commandID := strings.TrimSpace(raw)
	if commandID == "" {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return "", fmt.Errorf("generate robot command_id: %w", err)
		}
		return "robot-" + hex.EncodeToString(random), nil
	}
	if len(commandID) > maxRobotCommandIDLength || !robotCommandIDPattern.MatchString(commandID) {
		return "", fmt.Errorf("command_id must start with a letter or digit, be at most %d characters, and use only letters, digits, dot, underscore, colon, or dash", maxRobotCommandIDLength)
	}
	return commandID, nil
}

func storeRobotCommandLocked(device *robotDeviceRuntime, commandID, fingerprint string, receipt robotMoveReceipt) {
	device.commands[commandID] = robotStoredCommand{fingerprint: fingerprint, receipt: receipt}
	device.commandOrder = append(device.commandOrder, commandID)
	if len(device.commandOrder) <= maxRobotCommandHistory {
		return
	}
	expired := device.commandOrder[0]
	device.commandOrder = device.commandOrder[1:]
	delete(device.commands, expired)
}

func (h *RobotRuntimeHub) clearArmingCommand(deviceName, commandID, outcome string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	device := h.devices[deviceName]
	if device == nil || device.active == nil || device.active.command.CommandID != commandID {
		return
	}
	device.active = nil
	device.mode = "idle"
	device.linearMPS = 0
	device.angularRPS = 0
	device.lastOutcome = outcome
}

func (h *RobotRuntimeHub) finishCommand(deviceName, commandID, outcome string) {
	h.mu.Lock()
	device := h.devices[deviceName]
	if device == nil || device.active == nil || device.active.command.CommandID != commandID {
		h.mu.Unlock()
		return
	}
	h.integrateActiveLocked(device, device.active.deadline)
	if device.active.timer != nil {
		device.active.timer.Stop()
	}
	driver := device.driver
	device.active = nil
	device.mode = "idle"
	device.linearMPS = 0
	device.angularRPS = 0
	device.lastOutcome = outcome
	h.mu.Unlock()

	stopErr := driver.Stop(context.Background())
	if stopErr != nil {
		h.mu.Lock()
		if current := h.devices[deviceName]; current != nil {
			current.mode = "fault"
			current.lastOutcome = "driver_stop_failed"
		}
		h.mu.Unlock()
	}
	record := robotAuditRecord{
		Device: deviceName, Driver: driver.Name(), Action: "move", CommandID: commandID,
		Outcome: outcome,
	}
	if stopErr != nil {
		record.Outcome = "driver_stop_failed"
		record.Reason = stopErr.Error()
	}
	_ = h.appendAudit(record)
}

type robotStopReceipt struct {
	OK               bool   `json:"ok"`
	Device           string `json:"device"`
	Driver           string `json:"driver"`
	CommandID        string `json:"command_id"`
	Status           string `json:"status"`
	StoppedCommandID string `json:"stopped_command_id,omitempty"`
	WasMoving        bool   `json:"was_moving"`
	Reason           string `json:"reason,omitempty"`
	Timestamp        string `json:"timestamp"`
}

type RobotStopReceipt = robotStopReceipt

func (h *RobotRuntimeHub) Stop(ctx context.Context, params map[string]interface{}) (robotStopReceipt, error) {
	commandID, err := normalizeRobotCommandID(configvalue.String(params, "command_id", ""))
	if err != nil {
		// Emergency stop must not be rejected because an optional identifier was
		// malformed. Generate a safe host-side identifier and continue.
		commandID, err = normalizeRobotCommandID("")
		if err != nil {
			return robotStopReceipt{}, err
		}
	}
	reason := strings.TrimSpace(configvalue.String(params, "reason", "operator or agent requested stop"))
	if len(reason) > maxRobotStopReasonLength {
		reason = reason[:maxRobotStopReasonLength]
	}

	h.mu.Lock()
	deviceName, device, err := h.resolveDeviceLocked(configvalue.String(params, "device", ""))
	if err != nil {
		h.mu.Unlock()
		return robotStopReceipt{}, err
	}
	now := time.Now()
	h.integrateActiveLocked(device, now)
	stoppedCommandID := ""
	wasMoving := device.active != nil
	if device.active != nil {
		stoppedCommandID = device.active.command.CommandID
		if device.active.timer != nil {
			device.active.timer.Stop()
		}
	}
	driver := device.driver
	device.active = nil
	device.mode = "stopped"
	device.softwareStop = true
	device.linearMPS = 0
	device.angularRPS = 0
	device.lastCommandID = commandID
	device.lastOutcome = "stopped"
	h.mu.Unlock()

	stopErr := driver.Stop(ctx)
	receipt := robotStopReceipt{
		OK:               stopErr == nil,
		Device:           deviceName,
		Driver:           driver.Name(),
		CommandID:        commandID,
		Status:           "stopped",
		StoppedCommandID: stoppedCommandID,
		WasMoving:        wasMoving,
		Reason:           reason,
		Timestamp:        now.UTC().Format(time.RFC3339Nano),
	}
	record := robotAuditRecord{
		Timestamp: now.UTC(), Device: deviceName, Driver: driver.Name(), Action: "stop",
		CommandID: commandID, Outcome: "stopped", Reason: reason,
	}
	if stoppedCommandID != "" {
		record.Values = map[string]interface{}{"stopped_command_id": stoppedCommandID}
	}
	if stopErr != nil {
		h.mu.Lock()
		if current := h.devices[deviceName]; current != nil {
			current.mode = "fault"
			current.lastOutcome = "driver_stop_failed"
		}
		h.mu.Unlock()
		record.Outcome = "driver_stop_failed"
		record.Reason = stopErr.Error()
		_ = h.appendAudit(record)
		return receipt, fmt.Errorf("robot stop reached driver %q but it reported an error: %w", driver.Name(), stopErr)
	}
	// Stop is never blocked by audit storage. Physical safety takes priority;
	// persistence is best-effort only after the driver has received the stop.
	_ = h.appendAudit(record)
	return receipt, nil
}

func (h *RobotRuntimeHub) Status(ctx context.Context, requestedDevice string) (map[string]interface{}, error) {
	h.mu.Lock()
	deviceName, device, err := h.resolveDeviceLocked(requestedDevice)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	h.integrateActiveLocked(device, time.Now())
	driver := device.driver
	activeCommandID := ""
	deadline := ""
	if device.active != nil {
		activeCommandID = device.active.command.CommandID
		deadline = device.active.deadline.UTC().Format(time.RFC3339Nano)
	}
	snapshot := map[string]interface{}{
		"ok":                true,
		"device":            deviceName,
		"driver":            driver.Name(),
		"mode":              device.mode,
		"software_stopped":  device.softwareStop,
		"motion_enabled":    h.config.Enabled && h.config.AllowMotion && device.config.AllowMotion,
		"active_command_id": activeCommandID,
		"command_deadline":  deadline,
		"last_command_id":   device.lastCommandID,
		"last_outcome":      device.lastOutcome,
		"pose_estimate": map[string]interface{}{
			"x_m":         device.x,
			"y_m":         device.y,
			"heading_deg": device.headingDeg,
		},
		"velocity": map[string]interface{}{
			"linear_mps":  device.linearMPS,
			"angular_rps": device.angularRPS,
		},
		"safety": map[string]interface{}{
			"e_stop":           false,
			"obstacle_blocked": false,
			"tilt_fault":       false,
			"watchdog_ms":      device.config.WatchdogMS,
		},
		"limits": map[string]interface{}{
			"max_command_ms":   device.config.MaxCommandMS,
			"max_linear_mps":   device.config.MaxLinearMPS,
			"max_angular_rps":  device.config.MaxAngularRPS,
			"max_distance_m":   device.config.MaxDistanceM,
			"max_turn_degrees": device.config.MaxTurnDegrees,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	h.mu.Unlock()

	connected, detail, driverErr := driver.Status(ctx)
	snapshot["connected"] = connected
	snapshot["driver_detail"] = detail
	if driverErr != nil {
		snapshot["ok"] = false
		snapshot["driver_error"] = driverErr.Error()
		return snapshot, fmt.Errorf("robot driver status failed: %w", driverErr)
	}
	return snapshot, nil
}

type RobotStatusTool struct{ hub *RobotRuntimeHub }

func (t *RobotStatusTool) Name() string { return "robot_status" }
func (t *RobotStatusTool) Description() string {
	return "Read a configured robot's connection, motion mode, safety state, active command, estimated pose, velocity, and hard limits. " +
		"Call this before any robot motion and after each command. It never writes to the robot."
}
func (t *RobotStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"device": map[string]interface{}{
				"type":        "string",
				"description": "Configured robot device name. Omit only when tools_config.robot.default_device is set.",
			},
		},
	}
}
func (t *RobotStatusTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	status, err := t.hub.Status(ctx, configvalue.String(params, "device", ""))
	if err != nil && status == nil {
		return "", err
	}
	encoded, encodeErr := json.Marshal(status)
	if encodeErr != nil {
		return "", encodeErr
	}
	return string(encoded), err
}
func (t *RobotStatusTool) IsReadOnly() bool   { return true }
func (t *RobotStatusTool) MaxResultSize() int { return 8000 }

type RobotStopTool struct{ hub *RobotRuntimeHub }

func (t *RobotStopTool) Name() string { return "robot_stop" }
func (t *RobotStopTool) Description() string {
	return "Immediately send the configured robot driver a software stop, cancel the active bounded motion, and zero commanded velocity. " +
		"Use this for any stop intent, uncertainty, obstacle report, lost control, or safety concern. Stop bypasses motion permission gates and is never blocked by audit logging."
}
func (t *RobotStopTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"device": map[string]interface{}{
				"type":        "string",
				"description": "Configured robot device name; defaults to tools_config.robot.default_device.",
			},
			"command_id": map[string]interface{}{
				"type":        "string",
				"maxLength":   maxRobotCommandIDLength,
				"description": "Optional caller-generated command ID. A safe ID is generated when omitted or malformed so emergency stop is not delayed.",
			},
			"reason": map[string]interface{}{
				"type":        "string",
				"maxLength":   maxRobotStopReasonLength,
				"description": "Short audit reason for the stop.",
			},
		},
	}
}
func (t *RobotStopTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	receipt, err := t.hub.Stop(ctx, params)
	if err != nil && receipt.Device == "" {
		return "", err
	}
	encoded, encodeErr := json.Marshal(receipt)
	if encodeErr != nil {
		return "", encodeErr
	}
	return string(encoded), err
}
func (t *RobotStopTool) IsReadOnly() bool      { return false }
func (t *RobotStopTool) IsNeverParallel() bool { return true }
func (t *RobotStopTool) MaxResultSize() int    { return 6000 }

type RobotMoveTool struct{ hub *RobotRuntimeHub }

func (t *RobotMoveTool) Name() string { return "robot_move" }
func (t *RobotMoveTool) Description() string {
	return "Start one short, bounded mobile-robot motion through the safety runtime. " +
		"Use translate with signed distance_m for forward/backward, or rotate with signed angle_deg for left/right. " +
		"Never use this tool for continuous unattended driving, arbitrary hardware access, navigation, or arm motion. " +
		"Call robot_status first; on uncertainty or a new stop instruction call robot_stop immediately."
}
func (t *RobotMoveTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"device": map[string]interface{}{
				"type":        "string",
				"description": "Configured robot device name; defaults to tools_config.robot.default_device.",
			},
			"command_id": map[string]interface{}{
				"type":        "string",
				"maxLength":   maxRobotCommandIDLength,
				"description": "Stable unique ID for idempotent retries. The host generates one when omitted.",
			},
			"motion": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"translate", "rotate"},
				"description": "translate for forward/backward distance; rotate for in-place left/right turn.",
			},
			"distance_m": map[string]interface{}{
				"type":        "number",
				"description": "Signed metres for translate. Positive is forward; negative is backward. Runtime limits apply.",
			},
			"linear_mps": map[string]interface{}{
				"type":             "number",
				"exclusiveMinimum": 0,
				"description":      "Optional positive translation speed in metres per second. Direction comes from distance_m.",
			},
			"angle_deg": map[string]interface{}{
				"type":        "number",
				"description": "Signed degrees for rotate. Positive is left/counter-clockwise; negative is right/clockwise.",
			},
			"angular_rps": map[string]interface{}{
				"type":             "number",
				"exclusiveMinimum": 0,
				"description":      "Optional positive angular speed in radians per second. Direction comes from angle_deg.",
			},
			"timeout_ms": map[string]interface{}{
				"type":        "integer",
				"minimum":     100,
				"maximum":     60000,
				"description": "Optional stricter command timeout. It cannot exceed the configured safety limit.",
			},
		},
		"required": []string{"motion"},
	}
}
func (t *RobotMoveTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	receipt, err := t.hub.Move(ctx, params)
	if err != nil {
		return "", err
	}
	encoded, encodeErr := json.Marshal(receipt)
	if encodeErr != nil {
		return "", encodeErr
	}
	return string(encoded), nil
}
func (t *RobotMoveTool) IsReadOnly() bool      { return false }
func (t *RobotMoveTool) IsNeverParallel() bool { return true }
func (t *RobotMoveTool) MaxResultSize() int    { return 6000 }

func newRobotToolsForHub(hub *RobotRuntimeHub) []toolcontract.Tool {
	return []toolcontract.Tool{
		&RobotStatusTool{hub: hub},
		&RobotStopTool{hub: hub},
		&RobotMoveTool{hub: hub},
		&RobotQuickCommandTool{hub: hub},
	}
}

func NewRobotToolsForHub(hub *RobotRuntimeHub) []toolcontract.Tool {
	return newRobotToolsForHub(hub)
}

func newRobotTools(workspace string, config RobotToolConfig) []toolcontract.Tool {
	return newRobotToolsForHub(robotRuntimeHubFor(workspace, config))
}

func NewRobotTools(workspace string, config RobotToolConfig) []toolcontract.Tool {
	return newRobotTools(workspace, config)
}
