package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	devicePort          = 7000
	defaultScanWait     = 2 * time.Second
	defaultUpdateSource = "https://github.com/avyayv/personal-automation-cli.git"
)

var (
	modeChoices = map[string]int{"auto": 0, "cool": 1, "dry": 2, "fan": 3, "heat": 4}
	fanChoices  = map[string]int{"auto": 0, "low": 1, "medium-low": 2, "medium": 3, "medium-high": 4, "high": 5}
	modeNames   = map[int]string{0: "auto", 1: "cool", 2: "dry", 3: "fan", 4: "heat"}
	fanNames    = map[int]string{0: "auto", 1: "low", 2: "medium-low", 3: "medium", 4: "medium-high", 5: "high"}
	unitNames   = map[int]string{0: "C", 1: "F"}
)

type anyMap map[string]any

type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() < 1 {
		usage()
		os.Exit(2)
	}

	payload, code, err := runCLI(flag.Args(), NewService(LiveAdapter{}), NewConfigStore(defaultConfigPath()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if ce, ok := err.(codedError); ok {
			os.Exit(ce.code)
		}
		os.Exit(code)
	}
	printJSON(payload)
}

func usage() {
	fmt.Fprintf(os.Stderr, `GREE CLI (Go, no Python required)

Usage: gree <command> [args]

Commands:
  devices [--scan-wait seconds]           Discover GREE HVAC units
  status [--ip ip] [--mac mac]            Read current device status
  temp <degrees> [--ip ip] [--mac mac]    Set target temperature
  on [--ip ip] [--mac mac]                Turn the selected device on
  off [--ip ip] [--mac mac]               Turn the selected device off
  mode <auto|cool|dry|fan|heat>           Set operating mode
  fan <auto|low|medium-low|medium|medium-high|high>
  set temp|power|mode|fan ...             Long-form setter aliases
  config init                             Create the config file
  config show                             Show config values
  config set-device --ip ip|--mac mac     Save the default target device
  config clear-device                     Clear the default target device
  config set scan-wait <seconds>          Save the default discovery timeout
  update [install_path]                   Download, rebuild, and install latest CLI

All command output is JSON. Set GREE_CONFIG to override the config path.
`)
}

func runCLI(args []string, service Service, store ConfigStore) (any, int, error) {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "devices":
		opts, positionals, err := parseCommonArgs(rest, false)
		if err != nil {
			return nil, 1, err
		}
		if len(positionals) != 0 {
			return nil, 1, errors.New("usage: gree devices [--scan-wait seconds]")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		records, err := service.ListDevices(effectiveScanWait(opts, config))
		if err != nil {
			return nil, 1, err
		}
		return records, 0, nil
	case "status":
		opts, positionals, err := parseCommonArgs(rest, true)
		if err != nil {
			return nil, 1, err
		}
		if len(positionals) != 0 {
			return nil, 1, errors.New("usage: gree status [--ip ip] [--mac mac]")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		status, err := service.GetStatus(selectorFromOptions(opts), config, effectiveScanWait(opts, config))
		if err != nil {
			return nil, 1, err
		}
		return status, 0, nil
	case "temp":
		return handleSetAlias(rest, "temp", service, store)
	case "on":
		return handleSetValue(rest, "power", true, service, store)
	case "off":
		return handleSetValue(rest, "power", false, service, store)
	case "mode":
		return handleSetAlias(rest, "mode", service, store)
	case "fan":
		return handleSetAlias(rest, "fan", service, store)
	case "set":
		return handleSet(rest, service, store)
	case "config":
		return handleConfig(rest, service, store)
	case "update":
		payload, err := updateCLI(rest)
		if err != nil {
			return nil, 1, err
		}
		return payload, 0, nil
	default:
		return nil, 1, fmt.Errorf("unknown command %q", cmd)
	}
}

func handleSet(args []string, service Service, store ConfigStore) (any, int, error) {
	if len(args) < 1 {
		return nil, 1, errors.New("usage: gree set <temp|power|mode|fan> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "temp", "mode", "fan":
		return handleSetAlias(rest, sub, service, store)
	case "power":
		opts, positionals, err := parseCommonArgs(rest, true)
		if err != nil {
			return nil, 1, err
		}
		if len(positionals) != 1 || (positionals[0] != "on" && positionals[0] != "off") {
			return nil, 1, errors.New("usage: gree set power <on|off> [--ip ip] [--mac mac]")
		}
		return applyCommand(opts, "power", positionals[0] == "on", service, store)
	default:
		return nil, 1, fmt.Errorf("unknown set command %q", sub)
	}
}

func handleSetAlias(args []string, command string, service Service, store ConfigStore) (any, int, error) {
	opts, positionals, err := parseCommonArgs(args, true)
	if err != nil {
		return nil, 1, err
	}
	if len(positionals) != 1 {
		return nil, 1, fmt.Errorf("usage: gree %s <value> [--ip ip] [--mac mac]", command)
	}
	value := any(positionals[0])
	switch command {
	case "temp":
		temp, err := strconv.Atoi(positionals[0])
		if err != nil {
			return nil, 1, errors.New("temperature must be an integer")
		}
		value = temp
	case "mode":
		if _, ok := modeChoices[positionals[0]]; !ok {
			return nil, 1, fmt.Errorf("unsupported mode %q", positionals[0])
		}
	case "fan":
		if _, ok := fanChoices[positionals[0]]; !ok {
			return nil, 1, fmt.Errorf("unsupported fan speed %q", positionals[0])
		}
	}
	return applyCommand(opts, command, value, service, store)
}

func handleSetValue(args []string, command string, value any, service Service, store ConfigStore) (any, int, error) {
	opts, positionals, err := parseCommonArgs(args, true)
	if err != nil {
		return nil, 1, err
	}
	if len(positionals) != 0 {
		return nil, 1, errors.New("unexpected positional argument")
	}
	return applyCommand(opts, command, value, service, store)
}

func applyCommand(opts commonOptions, command string, value any, service Service, store ConfigStore) (any, int, error) {
	config, err := store.Load()
	if err != nil {
		return nil, 1, err
	}
	result, err := service.Apply(selectorFromOptions(opts), config, effectiveScanWait(opts, config), command, value)
	if err != nil {
		var verify VerificationError
		if errors.As(err, &verify) {
			return nil, 2, codedError{code: 2, err: err}
		}
		return nil, 1, err
	}
	return result, 0, nil
}

func handleConfig(args []string, service Service, store ConfigStore) (any, int, error) {
	if len(args) < 1 {
		return nil, 1, errors.New("usage: gree config <init|show|set-device|clear-device|set>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "init":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: gree config init")
		}
		created, err := store.Init()
		if err != nil {
			return nil, 1, err
		}
		return anyMap{"path": store.Path(), "created": created}, 0, nil
	case "show":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: gree config show")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		payload := config.ToMap()
		payload["path"] = store.Path()
		payload["exists"] = store.Exists()
		return payload, 0, nil
	case "set-device":
		opts, positionals, err := parseCommonArgs(rest, true)
		if err != nil {
			return nil, 1, err
		}
		if len(positionals) != 0 || (opts.IP == "" && opts.MAC == "") {
			return nil, 1, errors.New("usage: gree config set-device --ip ip|--mac mac")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		record, err := service.ResolveDevice(selectorFromOptions(opts), GreeConfig{}, effectiveScanWait(opts, config))
		if err != nil {
			return nil, 1, err
		}
		config.PreferredMAC = record.MAC
		config.PreferredIP = ""
		if config.PreferredMAC == "" {
			config.PreferredIP = record.IP
		}
		if err := store.Save(config); err != nil {
			return nil, 1, err
		}
		return anyMap{"preferred_mac": config.PreferredMAC, "preferred_ip": config.PreferredIP, "device": record}, 0, nil
	case "clear-device":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: gree config clear-device")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		config.PreferredMAC = ""
		config.PreferredIP = ""
		if err := store.Save(config); err != nil {
			return nil, 1, err
		}
		return anyMap{"preferred_mac": nil, "preferred_ip": nil}, 0, nil
	case "set":
		if len(rest) != 2 || rest[0] != "scan-wait" {
			return nil, 1, errors.New("usage: gree config set scan-wait <seconds>")
		}
		seconds, err := strconv.ParseFloat(rest[1], 64)
		if err != nil || seconds <= 0 {
			return nil, 1, errors.New("scan-wait must be a positive number")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		config.ScanWaitSeconds = seconds
		if err := store.Save(config); err != nil {
			return nil, 1, err
		}
		return config.ToMap(), 0, nil
	default:
		return nil, 1, fmt.Errorf("unknown config command %q", sub)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

type commonOptions struct {
	IP       string
	MAC      string
	ScanWait *time.Duration
}

func parseCommonArgs(args []string, selectors bool) (commonOptions, []string, error) {
	var opts commonOptions
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--ip":
			if !selectors {
				return opts, nil, errors.New("--ip is not valid for this command")
			}
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--ip requires a value")
				}
				value = args[i]
			}
			opts.IP = value
		case "--mac":
			if !selectors {
				return opts, nil, errors.New("--mac is not valid for this command")
			}
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--mac requires a value")
				}
				value = args[i]
			}
			opts.MAC = normalizeMAC(value)
		case "--scan-wait":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--scan-wait requires a value")
				}
				value = args[i]
			}
			seconds, err := strconv.ParseFloat(value, 64)
			if err != nil || seconds <= 0 {
				return opts, nil, errors.New("--scan-wait must be a positive number")
			}
			d := time.Duration(seconds * float64(time.Second))
			opts.ScanWait = &d
		default:
			if strings.HasPrefix(arg, "--") {
				return opts, nil, fmt.Errorf("unknown flag %q", arg)
			}
			pos = append(pos, arg)
		}
	}
	return opts, pos, nil
}

func selectorFromOptions(opts commonOptions) DeviceSelector {
	return DeviceSelector{IP: opts.IP, MAC: opts.MAC}.Normalized()
}

func effectiveScanWait(opts commonOptions, config GreeConfig) time.Duration {
	if opts.ScanWait != nil {
		return *opts.ScanWait
	}
	return config.ScanWait()
}

type GreeConfig struct {
	PreferredMAC    string
	PreferredIP     string
	ScanWaitSeconds float64
}

func (c GreeConfig) ScanWait() time.Duration {
	if c.ScanWaitSeconds <= 0 {
		return defaultScanWait
	}
	return time.Duration(c.ScanWaitSeconds * float64(time.Second))
}

func (c GreeConfig) ToMap() anyMap {
	return anyMap{
		"preferred_mac": emptyNil(c.PreferredMAC),
		"preferred_ip":  emptyNil(c.PreferredIP),
		"scan_wait":     c.ScanWait().Seconds(),
	}
}

type ConfigStore interface {
	Path() string
	Exists() bool
	Load() (GreeConfig, error)
	Init() (bool, error)
	Save(GreeConfig) error
}

type FileConfigStore struct{ path string }

func NewConfigStore(path string) FileConfigStore { return FileConfigStore{path: path} }
func (s FileConfigStore) Path() string           { return s.path }
func (s FileConfigStore) Exists() bool           { _, err := os.Stat(s.path); return err == nil }

func (s FileConfigStore) Load() (GreeConfig, error) {
	config := GreeConfig{ScanWaitSeconds: defaultScanWait.Seconds()}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.Split(raw, "#")[0])
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return config, fmt.Errorf("invalid config line %d", lineNo+1)
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch key {
		case "scan_wait":
			seconds, err := strconv.ParseFloat(val, 64)
			if err != nil || seconds <= 0 {
				return config, fmt.Errorf("invalid scan_wait on line %d", lineNo+1)
			}
			config.ScanWaitSeconds = seconds
		case "preferred_mac":
			config.PreferredMAC = normalizeMAC(val)
		case "preferred_ip":
			config.PreferredIP = val
		default:
			return config, fmt.Errorf("unknown config key %q on line %d", key, lineNo+1)
		}
	}
	return config, nil
}

func (s FileConfigStore) Init() (bool, error) {
	if s.Exists() {
		return false, nil
	}
	return true, s.Save(GreeConfig{ScanWaitSeconds: defaultScanWait.Seconds()})
}

func (s FileConfigStore) Save(config GreeConfig) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Defaults for the gree CLI.\n")
	b.WriteString(fmt.Sprintf("scan_wait = %.3g\n", config.ScanWait().Seconds()))
	if config.PreferredMAC != "" {
		b.WriteString(fmt.Sprintf("preferred_mac = %q\n", normalizeMAC(config.PreferredMAC)))
	}
	if config.PreferredIP != "" {
		b.WriteString(fmt.Sprintf("preferred_ip = %q\n", config.PreferredIP))
	}
	return os.WriteFile(s.path, []byte(b.String()), 0o644)
}

func defaultConfigPath() string {
	if path := os.Getenv("GREE_CONFIG"); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "gree", "config.toml")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "gree", "config.toml")
	}
	return "config.toml"
}

type DeviceSelector struct {
	IP  string
	MAC string
}

func (s DeviceSelector) Normalized() DeviceSelector {
	return DeviceSelector{IP: s.IP, MAC: normalizeMAC(s.MAC)}
}

func (s DeviceSelector) IsSet() bool { return s.IP != "" || s.MAC != "" }

type DeviceRecord struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	MAC     string `json:"mac"`
	Name    string `json:"name,omitempty"`
	Brand   string `json:"brand,omitempty"`
	Model   string `json:"model,omitempty"`
	Version string `json:"version,omitempty"`
}

type DeviceHandle struct {
	Record DeviceRecord
	Raw    any
}

type StatusRecord struct {
	IP                 string `json:"ip"`
	Port               int    `json:"port"`
	MAC                string `json:"mac"`
	Name               string `json:"name,omitempty"`
	Brand              string `json:"brand,omitempty"`
	Model              string `json:"model,omitempty"`
	Version            string `json:"version,omitempty"`
	Power              *bool  `json:"power"`
	Mode               string `json:"mode,omitempty"`
	CurrentTemperature *int   `json:"current_temperature"`
	TargetTemperature  *int   `json:"target_temperature"`
	Units              string `json:"units,omitempty"`
	FanSpeed           string `json:"fan_speed,omitempty"`
}

type ChangeResult struct {
	Before StatusRecord `json:"before"`
	After  StatusRecord `json:"after"`
}

type Adapter interface {
	Discover(scanWait time.Duration) ([]DeviceHandle, error)
	ReadStatus(handle DeviceHandle) (StatusRecord, error)
	Apply(handle DeviceHandle, command string, value any) (ChangeResult, error)
}

type Service struct{ adapter Adapter }

func NewService(adapter Adapter) Service { return Service{adapter: adapter} }

func (s Service) ListDevices(scanWait time.Duration) ([]DeviceRecord, error) {
	handles, err := s.adapter.Discover(scanWait)
	if err != nil {
		return nil, err
	}
	records := make([]DeviceRecord, 0, len(handles))
	for _, handle := range handles {
		records = append(records, handle.Record)
	}
	return records, nil
}

func (s Service) GetStatus(selector DeviceSelector, config GreeConfig, scanWait time.Duration) (StatusRecord, error) {
	handle, err := s.resolveHandle(selector, config, scanWait)
	if err != nil {
		return StatusRecord{}, err
	}
	return s.adapter.ReadStatus(handle)
}

func (s Service) Apply(selector DeviceSelector, config GreeConfig, scanWait time.Duration, command string, value any) (ChangeResult, error) {
	handle, err := s.resolveHandle(selector, config, scanWait)
	if err != nil {
		return ChangeResult{}, err
	}
	result, err := s.adapter.Apply(handle, command, value)
	if err != nil {
		return ChangeResult{}, err
	}
	if err := verifyChange(result.After, command, value); err != nil {
		return ChangeResult{}, err
	}
	return result, nil
}

func (s Service) ResolveDevice(selector DeviceSelector, config GreeConfig, scanWait time.Duration) (DeviceRecord, error) {
	handle, err := s.resolveHandle(selector, config, scanWait)
	if err != nil {
		return DeviceRecord{}, err
	}
	return handle.Record, nil
}

func (s Service) resolveHandle(selector DeviceSelector, config GreeConfig, scanWait time.Duration) (DeviceHandle, error) {
	handles, err := s.adapter.Discover(scanWait)
	if err != nil {
		return DeviceHandle{}, err
	}
	if len(handles) == 0 {
		return DeviceHandle{}, errors.New("no GREE devices found")
	}
	explicit := selector.Normalized()
	configured := DeviceSelector{IP: config.PreferredIP, MAC: config.PreferredMAC}.Normalized()
	if explicit.IsSet() {
		return matchSingle(handles, explicit, "no GREE device matched the requested selectors", "multiple GREE devices matched the requested selectors")
	}
	if configured.IsSet() {
		return matchSingle(handles, configured, "no GREE device matched the configured preferred device", "configured preferred device matched multiple GREE units")
	}
	if len(handles) == 1 {
		return handles[0], nil
	}
	choices := make([]string, 0, len(handles))
	for _, handle := range handles {
		choices = append(choices, handle.Record.IP+"/"+handle.Record.MAC)
	}
	return DeviceHandle{}, fmt.Errorf("multiple GREE devices matched; rerun with --ip or --mac; matches: %s", strings.Join(choices, ", "))
}

func matchSingle(handles []DeviceHandle, selector DeviceSelector, noneMessage, manyMessage string) (DeviceHandle, error) {
	matches := make([]DeviceHandle, 0, len(handles))
	for _, handle := range handles {
		if selector.IP != "" && handle.Record.IP != selector.IP {
			continue
		}
		if selector.MAC != "" && normalizeMAC(handle.Record.MAC) != selector.MAC {
			continue
		}
		matches = append(matches, handle)
	}
	if len(matches) == 0 {
		return DeviceHandle{}, errors.New(noneMessage)
	}
	if len(matches) > 1 {
		return DeviceHandle{}, errors.New(manyMessage)
	}
	return matches[0], nil
}

type VerificationError struct{ message string }

func (e VerificationError) Error() string { return e.message }

func verifyChange(status StatusRecord, command string, value any) error {
	switch command {
	case "temp":
		expected := value.(int)
		if status.TargetTemperature == nil || *status.TargetTemperature != expected {
			return VerificationError{message: "device reported target temperature update, but read-back verification failed"}
		}
	case "power":
		expected := value.(bool)
		if status.Power == nil || *status.Power != expected {
			return VerificationError{message: "device reported power update, but read-back verification failed"}
		}
	case "mode":
		expected := value.(string)
		if status.Mode != expected {
			return VerificationError{message: "device reported mode update, but read-back verification failed"}
		}
	case "fan":
		expected := value.(string)
		if status.FanSpeed != expected {
			return VerificationError{message: "device reported fan speed update, but read-back verification failed"}
		}
	}
	return nil
}

type LiveAdapter struct{}

type liveHandle struct {
	Info DeviceRecord
	Key  string
	V2   bool
}

func (LiveAdapter) Discover(scanWait time.Duration) ([]DeviceHandle, error) {
	conn, err := listenUDP(true, "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	packet, _ := json.Marshal(anyMap{"t": "scan"})
	for _, ip := range broadcastAddresses() {
		_, _ = conn.WriteToUDP(packet, &net.UDPAddr{IP: ip, Port: devicePort})
	}

	deadline := time.Now().Add(scanWait)
	seen := map[string]DeviceHandle{}
	buf := make([]byte, 8192)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			return nil, err
		}
		outer, pack, err := decodePacket(buf[:n], newCipherV1())
		if err != nil || pack == nil {
			_ = outer
			continue
		}
		mac := firstString(pack, "mac", "cid")
		mac = normalizeMAC(mac)
		if mac == "" {
			continue
		}
		record := DeviceRecord{
			IP:      addr.IP.String(),
			Port:    addr.Port,
			MAC:     mac,
			Name:    stringValue(pack["name"]),
			Brand:   stringValue(pack["brand"]),
			Model:   stringValue(pack["model"]),
			Version: stringValue(pack["ver"]),
		}
		seen[mac] = DeviceHandle{Record: record, Raw: liveHandle{Info: record}}
	}
	handles := make([]DeviceHandle, 0, len(seen))
	for _, handle := range seen {
		handles = append(handles, handle)
	}
	sort.Slice(handles, func(i, j int) bool { return handles[i].Record.MAC < handles[j].Record.MAC })
	return handles, nil
}

func (a LiveAdapter) ReadStatus(handle DeviceHandle) (StatusRecord, error) {
	live, err := a.boundHandle(handle)
	if err != nil {
		return StatusRecord{}, err
	}
	props, version, err := readProperties(live)
	if err != nil {
		return StatusRecord{}, err
	}
	return statusFromProperties(live.Info, props, version), nil
}

func (a LiveAdapter) Apply(handle DeviceHandle, command string, value any) (ChangeResult, error) {
	live, err := a.boundHandle(handle)
	if err != nil {
		return ChangeResult{}, err
	}
	beforeProps, beforeVersion, err := readProperties(live)
	if err != nil {
		return ChangeResult{}, err
	}
	before := statusFromProperties(live.Info, beforeProps, beforeVersion)

	cmdProps, err := commandProperties(command, value, beforeProps)
	if err != nil {
		return ChangeResult{}, err
	}
	if err := sendCommand(live, cmdProps); err != nil {
		return ChangeResult{}, err
	}
	time.Sleep(500 * time.Millisecond)

	afterProps, afterVersion, err := readProperties(live)
	if err != nil {
		return ChangeResult{}, err
	}
	after := statusFromProperties(live.Info, afterProps, afterVersion)
	return ChangeResult{Before: before, After: after}, nil
}

func (a LiveAdapter) boundHandle(handle DeviceHandle) (liveHandle, error) {
	if live, ok := handle.Raw.(liveHandle); ok && live.Key != "" {
		return live, nil
	}
	info := handle.Record
	key, v2, err := bindDevice(info)
	if err != nil {
		return liveHandle{}, err
	}
	return liveHandle{Info: info, Key: key, V2: v2}, nil
}

func bindDevice(info DeviceRecord) (string, bool, error) {
	for _, c := range []greeCipher{newCipherV1(), newCipherV2()} {
		msg := basePayload("bind", info.MAC, anyMap{"uid": 0}, true)
		outer, err := roundTrip(info, msg, c, 4*time.Second)
		if err != nil {
			continue
		}
		pack, _ := outer["pack"].(map[string]any)
		if pack != nil && stringValue(pack["t"]) == "bindok" {
			key := stringValue(pack["key"])
			if key != "" {
				return key, c.version() == 2, nil
			}
		}
	}
	return "", false, fmt.Errorf("failed to bind to %s/%s", info.IP, info.MAC)
}

var statusColumns = []string{
	"Pow", "Mod", "Dwet", "DwatSen", "Dfltr", "DwatFul", "Dmod", "SetTem", "TemSen", "TemUn", "TemRec", "WdSpd", "Air", "Blo", "Health", "SwhSlp", "SlpMod", "Lig", "SwingLfRig", "SwUpDn", "Quiet", "Tur", "StHt", "SvSt", "HeatCoolType", "hid",
}

func readProperties(live liveHandle) (map[string]int, string, error) {
	msg := basePayload("status", live.Info.MAC, anyMap{"cols": statusColumns}, false)
	outer, err := roundTrip(live.Info, msg, cipherForBound(live), 5*time.Second)
	if err != nil {
		return nil, "", err
	}
	pack, _ := outer["pack"].(map[string]any)
	if pack == nil || stringValue(pack["t"]) != "dat" {
		return nil, "", errors.New("unexpected status response from device")
	}
	cols, ok1 := stringSlice(pack["cols"])
	dat, ok2 := anySlice(pack["dat"])
	if !ok1 || !ok2 {
		return nil, "", errors.New("status response missing cols/dat")
	}
	props := map[string]int{}
	version := ""
	for i, col := range cols {
		if i >= len(dat) {
			break
		}
		if col == "hid" {
			version = parseVersion(stringValue(dat[i]))
			continue
		}
		if n, ok := intValue(dat[i]); ok {
			props[col] = n
		}
	}
	if temp, ok := props["TemSen"]; ok && temp < 40 {
		version = "4.0"
	}
	return props, version, nil
}

func sendCommand(live liveHandle, props map[string]int) error {
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]int, 0, len(keys))
	for _, key := range keys {
		values = append(values, props[key])
	}
	msg := basePayload("cmd", live.Info.MAC, anyMap{"opt": keys, "p": values}, false)
	outer, err := roundTrip(live.Info, msg, cipherForBound(live), 5*time.Second)
	if err != nil {
		return err
	}
	pack, _ := outer["pack"].(map[string]any)
	if pack == nil || stringValue(pack["t"]) != "res" {
		return errors.New("unexpected command response from device")
	}
	return nil
}

func commandProperties(command string, value any, current map[string]int) (map[string]int, error) {
	switch command {
	case "temp":
		temp := value.(int)
		if current["TemUn"] == 1 {
			rec, err := generateTemperatureRecord(temp)
			if err != nil {
				return nil, err
			}
			return map[string]int{"SetTem": rec.temSet, "TemRec": rec.temRec, "TemUn": 1}, nil
		}
		if temp < 8 || temp > 30 {
			return nil, errors.New("celsius temperature must be between 8 and 30")
		}
		return map[string]int{"SetTem": temp}, nil
	case "power":
		if value.(bool) {
			return map[string]int{"Pow": 1}, nil
		}
		return map[string]int{"Pow": 0}, nil
	case "mode":
		mode, ok := modeChoices[value.(string)]
		if !ok {
			return nil, fmt.Errorf("unsupported mode %q", value)
		}
		return map[string]int{"Mod": mode}, nil
	case "fan":
		fan, ok := fanChoices[value.(string)]
		if !ok {
			return nil, fmt.Errorf("unsupported fan speed %q", value)
		}
		return map[string]int{"WdSpd": fan}, nil
	default:
		return nil, fmt.Errorf("unsupported command %q", command)
	}
}

func statusFromProperties(info DeviceRecord, props map[string]int, version string) StatusRecord {
	power := boolPtrFromInt(props, "Pow")
	mode := ""
	if n, ok := props["Mod"]; ok {
		mode = modeNames[n]
	}
	units := ""
	if n, ok := props["TemUn"]; ok {
		units = unitNames[n]
	}
	fan := ""
	if n, ok := props["WdSpd"]; ok {
		fan = fanNames[n]
	}
	target := targetTemperature(props)
	current := currentTemperature(props, version, target)
	return StatusRecord{IP: info.IP, Port: info.Port, MAC: info.MAC, Name: info.Name, Brand: info.Brand, Model: info.Model, Version: info.Version, Power: power, Mode: mode, CurrentTemperature: current, TargetTemperature: target, Units: units, FanSpeed: fan}
}

func targetTemperature(props map[string]int) *int {
	temSet, ok := props["SetTem"]
	if !ok {
		return nil
	}
	if props["TemUn"] == 1 {
		bit := props["TemRec"]
		v := convertToUnits(temSet, bit)
		return &v
	}
	return &temSet
}

func currentTemperature(props map[string]int, version string, fallback *int) *int {
	temSen, ok := props["TemSen"]
	if !ok {
		return fallback
	}
	bit := props["TemRec"]
	major := 0
	if version != "" {
		major, _ = strconv.Atoi(strings.Split(version, ".")[0])
	}
	if major == 4 {
		v := convertToUnits(temSen, bit)
		return &v
	}
	if temSen != 0 {
		v := convertToUnits(temSen-40, bit)
		return &v
	}
	return fallback
}

type tempRecord struct{ f, temSet, temRec int }

func generateTemperatureRecord(tempF int) (tempRecord, error) {
	c := (float64(tempF) - 32) * 5 / 9
	temSet := int(math.Round(c))
	temRec := 0
	if c-float64(temSet) > 0 {
		temRec = 1
	}
	if temSet < 8 || temSet > 30 {
		return tempRecord{}, errors.New("fahrenheit temperature must be between 46 and 86")
	}
	return tempRecord{f: tempF, temSet: temSet, temRec: temRec}, nil
}

func convertToUnits(value, bit int) int {
	if bit != 0 && bit != 1 {
		bit = 0
	}
	for f := -76; f <= 140; f++ {
		rec, _ := generateTemperatureRecordUnbounded(f)
		if rec.temSet == value && rec.temRec == bit {
			return rec.f
		}
	}
	for f := -76; f <= 140; f++ {
		rec, _ := generateTemperatureRecordUnbounded(f)
		if rec.temSet == value {
			return rec.f
		}
	}
	return value
}

func generateTemperatureRecordUnbounded(tempF int) (tempRecord, error) {
	c := (float64(tempF) - 32) * 5 / 9
	temSet := int(math.Round(c))
	temRec := 0
	if c-float64(temSet) > 0 {
		temRec = 1
	}
	return tempRecord{f: tempF, temSet: temSet, temRec: temRec}, nil
}

func boolPtrFromInt(props map[string]int, key string) *bool {
	n, ok := props[key]
	if !ok {
		return nil
	}
	b := n != 0
	return &b
}

func basePayload(command, mac string, data anyMap, initial bool) anyMap {
	pack := anyMap{"t": command, "mac": mac}
	for k, v := range data {
		pack[k] = v
	}
	i := 0
	if initial {
		i = 1
	}
	return anyMap{"cid": "app", "i": i, "t": "pack", "uid": 0, "tcid": mac, "pack": pack}
}

func roundTrip(info DeviceRecord, msg anyMap, c greeCipher, timeout time.Duration) (anyMap, error) {
	conn, err := listenUDP(false, "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	packet, err := encodePacket(msg, c)
	if err != nil {
		return nil, err
	}
	_, err = conn.WriteToUDP(packet, &net.UDPAddr{IP: net.ParseIP(info.IP), Port: info.Port})
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 8192)
	deadline := time.Now().Add(timeout)
	for {
		_ = conn.SetReadDeadline(deadline)
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return nil, err
		}
		if !addr.IP.Equal(net.ParseIP(info.IP)) {
			continue
		}
		outer, _, err := decodePacket(buf[:n], c)
		return outer, err
	}
}

func listenUDP(broadcast bool, address string) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var ctrlErr error
		if err := c.Control(func(fd uintptr) {
			if broadcast {
				if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); err != nil && ctrlErr == nil {
					ctrlErr = err
				}
			}
			if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil && ctrlErr == nil {
				ctrlErr = err
			}
		}); err != nil {
			return err
		}
		return ctrlErr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", address)
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		return nil, errors.New("listen did not return UDP connection")
	}
	return conn, nil
}

func broadcastAddresses() []net.IP {
	seen := map[string]bool{}
	add := func(ip net.IP, out *[]net.IP) {
		if ip == nil {
			return
		}
		v4 := ip.To4()
		if v4 == nil || seen[v4.String()] {
			return
		}
		seen[v4.String()] = true
		*out = append(*out, v4)
	}
	var out []net.IP
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || len(ipNet.Mask) != net.IPv4len {
				continue
			}
			bcast := make(net.IP, net.IPv4len)
			for i := 0; i < net.IPv4len; i++ {
				bcast[i] = ip[i] | ^ipNet.Mask[i]
			}
			add(bcast, &out)
		}
	}
	add(net.IPv4(255, 255, 255, 255), &out)
	return out
}

type greeCipher interface {
	version() int
	encrypt(any) (pack string, tag string, err error)
	decrypt(pack string, tag string) (anyMap, error)
	withKey(key string) greeCipher
}

type cipherV1 struct{ key []byte }
type cipherV2 struct{ key []byte }

func newCipherV1() cipherV1                      { return cipherV1{key: []byte("a3K8Bx%2r8Y7#xDh")} }
func newCipherV2() cipherV2                      { return cipherV2{key: []byte("{yxAHAY_Lm6pbC/<")} }
func (c cipherV1) version() int                  { return 1 }
func (c cipherV2) version() int                  { return 2 }
func (c cipherV1) withKey(key string) greeCipher { return cipherV1{key: []byte(key)} }
func (c cipherV2) withKey(key string) greeCipher { return cipherV2{key: []byte(key)} }

func cipherForBound(live liveHandle) greeCipher {
	if live.V2 {
		return newCipherV2().withKey(live.Key)
	}
	return newCipherV1().withKey(live.Key)
}

func (c cipherV1) encrypt(v any) (string, string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", "", err
	}
	padded := pkcs7Pad(data, aes.BlockSize)
	out := make([]byte, len(padded))
	for start := 0; start < len(padded); start += aes.BlockSize {
		block.Encrypt(out[start:start+aes.BlockSize], padded[start:start+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(out), "", nil
}

func (c cipherV1) decrypt(pack, _ string) (anyMap, error) {
	data, err := base64.StdEncoding.DecodeString(pack)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, errors.New("invalid AES-ECB ciphertext length")
	}
	out := make([]byte, len(data))
	for start := 0; start < len(data); start += aes.BlockSize {
		block.Decrypt(out[start:start+aes.BlockSize], data[start:start+aes.BlockSize])
	}
	return jsonFromPadded(out)
}

func (c cipherV2) encrypt(v any) (string, string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 12)
	if err != nil {
		return "", "", err
	}
	nonce := []byte{0x54, 0x40, 0x78, 0x44, 0x49, 0x67, 0x5a, 0x51, 0x6c, 0x5e, 0x63, 0x13}
	sealed := gcm.Seal(nil, nonce, data, []byte("qualcomm-test"))
	ciphertext, tag := sealed[:len(sealed)-gcm.Overhead()], sealed[len(sealed)-gcm.Overhead():]
	return base64.StdEncoding.EncodeToString(ciphertext), base64.StdEncoding.EncodeToString(tag), nil
}

func (c cipherV2) decrypt(pack, tag string) (anyMap, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(pack)
	if err != nil {
		return nil, err
	}
	tagBytes, err := base64.StdEncoding.DecodeString(tag)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 12)
	if err != nil {
		return nil, err
	}
	nonce := []byte{0x54, 0x40, 0x78, 0x44, 0x49, 0x67, 0x5a, 0x51, 0x6c, 0x5e, 0x63, 0x13}
	plain, err := gcm.Open(nil, nonce, append(ciphertext, tagBytes...), []byte("qualcomm-test"))
	if err != nil {
		return nil, err
	}
	return jsonFromPadded(plain)
}

func encodePacket(msg anyMap, c greeCipher) ([]byte, error) {
	if pack, ok := msg["pack"]; ok {
		crypted, tag, err := c.encrypt(pack)
		if err != nil {
			return nil, err
		}
		msg = cloneMap(msg)
		msg["pack"] = crypted
		if tag != "" {
			msg["tag"] = tag
		}
	}
	return json.Marshal(msg)
}

func decodePacket(data []byte, c greeCipher) (anyMap, anyMap, error) {
	var outer anyMap
	if err := json.Unmarshal(data, &outer); err != nil {
		return nil, nil, err
	}
	packString := stringValue(outer["pack"])
	if packString == "" {
		return outer, nil, nil
	}
	pack, err := c.decrypt(packString, stringValue(outer["tag"]))
	if err != nil {
		return nil, nil, err
	}
	outer["pack"] = pack
	return outer, pack, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func jsonFromPadded(data []byte) (anyMap, error) {
	idx := bytes.LastIndexByte(data, '}')
	if idx >= 0 {
		data = data[:idx+1]
	}
	var out anyMap
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func updateCLI(args []string) (any, error) {
	if len(args) > 1 {
		return nil, errors.New("usage: gree update [install_path]")
	}
	target, err := updateTarget(args)
	if err != nil {
		return nil, err
	}
	previousChecksum, _ := fileSHA256(target)
	source := os.Getenv("GREE_CLI_UPDATE_SOURCE")
	if source == "" {
		source = os.Getenv("GREE_CLI_UPDATE_URL")
	}
	if source == "" {
		source = defaultUpdateSource
	}
	tmp, err := os.MkdirTemp("", "gree-cli-update-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	checkout := filepath.Join(tmp, "src")
	if err := fetchSource(source, checkout); err != nil {
		return nil, err
	}
	cliDir := filepath.Join(checkout, "cli", "gree-cli")
	if err := runCmd(cliDir, "go", "build", "-o", target, "."); err != nil {
		return nil, err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return nil, err
	}
	newChecksum, _ := fileSHA256(target)
	return anyMap{"installed": target, "source": source, "previous_sha256": previousChecksum, "sha256": newChecksum}, nil
}

func updateTarget(args []string) (string, error) {
	if len(args) == 1 {
		return filepath.Abs(args[0])
	}
	if exe, err := os.Executable(); err == nil && exe != "" && !strings.Contains(exe, string(os.PathSeparator)+"go-build") {
		return exe, nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "bin", "gree"), nil
	}
	return "", errors.New("could not determine install path")
}

func fetchSource(source, dest string) error {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if strings.HasSuffix(source, ".git") {
			return runCmd("", "git", "clone", "--depth", "1", source, dest)
		}
		return fetchZip(source, dest)
	}
	return runCmd("", "git", "clone", "--depth", "1", source, dest)
}

func fetchZip(source, dest string) error {
	resp, err := httpGet(source)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		target := filepath.Join(dest, parts[1])
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, f.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func httpGet(url string) (*http.Response, error) {
	// Kept as a function so tests can replace network access later without
	// changing updateCLI's control flow.
	return httpDefaultClient().Get(url)
}

func httpDefaultClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func normalizeMAC(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func cloneMap(in anyMap) anyMap {
	out := make(anyMap, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstString(m anyMap, keys ...string) string {
	for _, key := range keys {
		if s := stringValue(m[key]); s != "" {
			return s
		}
	}
	return ""
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func intValue(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func stringSlice(v any) ([]string, bool) {
	items, ok := anySlice(v)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, stringValue(item))
	}
	return out, true
}

func anySlice(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []string:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out, true
	case []int:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out, true
	default:
		return nil, false
	}
}

func parseVersion(hid string) string {
	re := regexp.MustCompile(`V([\d.]+)\.bin$`)
	match := re.FindStringSubmatch(hid)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}
