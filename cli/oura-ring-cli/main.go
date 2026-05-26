package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	apiBaseURL          = "https://api.ouraring.com"
	defaultUpdateSource = "https://github.com/avyayv/personal-automation-cli.git"
	redactedToken       = "********"
)

type anyMap map[string]any

type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

func main() {
	args := os.Args[1:]
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		usage()
		return
	}

	payload, opts, code, err := runCLIWithOptions(args, NewOuraClient(apiBaseURL), NewConfigStore(defaultConfigPath()), time.Now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if ce, ok := err.(codedError); ok {
			os.Exit(ce.code)
		}
		os.Exit(code)
	}
	printJSON(payload, opts.Pretty)
}

func usage() {
	fmt.Fprintf(os.Stderr, `Oura Ring CLI (Go, no Python required)

Usage: oura <command> [args]

Commands:
  personal-info                         Show account profile details
  doctor                                Check config, auth, and API reachability without exposing data
  ring-configuration                    Show ring configuration records
  daily-activity [date flags]           Fetch daily activity records
  daily-readiness [date flags]          Fetch daily readiness records
  daily-sleep [date flags]              Fetch daily sleep summary records
  daily-stress [date flags]             Fetch daily stress records
  daily-spo2 [date flags]               Fetch daily SpO2 records
  sleep [date flags]                    Fetch sleep period records
  heartrate [datetime flags]            Fetch heart-rate samples
  get <path> [--param key=value] [--raw] Call an Oura API path directly
  config init                           Create the config file
  config show                           Show config values (token redacted)
  config set-token <token>              Save a personal access token
  config clear-token                    Remove the saved token
  update [install_path]                 Download, rebuild, and install latest CLI

Date flags:
  --start-date YYYY-MM-DD               Start date for daily/sleep endpoints
  --end-date YYYY-MM-DD                 End date for daily/sleep endpoints
  --days N                              Default date window size (default: 7)
  --raw                                 Return the full API response instead of a concise summary

Datetime flags:
  --start-datetime RFC3339              Start timestamp for heartrate
  --end-datetime RFC3339                End timestamp for heartrate
  --raw                                 Return the full API response instead of a concise summary

Global flags:
  --select a,b.c                        Return only selected JSON fields (repeat or comma-separate)
  --output json|pretty                  Output compact JSON (default) or indented JSON
  --pretty                              Alias for --output pretty

Authentication:
  Set OURA_TOKEN or run: oura config set-token <personal-access-token>
  Create a token at https://cloud.ouraring.com/personal-access-tokens

Collection commands return compact summary JSON by default. Add --raw for full API responses.
Set OURA_CONFIG to override the config path.
`)
}

type cliOptions struct {
	Pretty bool
	Select []string
}

func runCLI(args []string, client OuraGetter, store ConfigStore, now func() time.Time) (any, int, error) {
	payload, _, code, err := runCLIWithOptions(args, client, store, now)
	return payload, code, err
}

func runCLIWithOptions(args []string, client OuraGetter, store ConfigStore, now func() time.Time) (any, cliOptions, int, error) {
	opts, cleanArgs, err := parseGlobalArgs(args)
	if err != nil {
		return nil, opts, 1, err
	}
	payload, code, err := runCoreCLI(cleanArgs, client, store, now)
	if err != nil || len(opts.Select) == 0 {
		return payload, opts, code, err
	}
	return selectPayloadFields(payload, opts.Select), opts, code, nil
}

func runCoreCLI(args []string, client OuraGetter, store ConfigStore, now func() time.Time) (any, int, error) {
	if len(args) < 1 {
		return nil, 2, errors.New("missing command")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "personal-info", "profile", "me":
		if len(rest) != 0 {
			return nil, 1, fmt.Errorf("usage: oura %s", cmd)
		}
		return getAuthorized(client, store, "/v2/usercollection/personal_info", nil)
	case "doctor":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: oura doctor")
		}
		return handleDoctor(client, store)
	case "ring-configuration", "rings":
		if len(rest) != 0 {
			return nil, 1, fmt.Errorf("usage: oura %s", cmd)
		}
		return getAuthorized(client, store, "/v2/usercollection/ring_configuration", nil)
	case "daily-activity":
		return handleDateCollection(rest, client, store, now, "/v2/usercollection/daily_activity")
	case "daily-readiness":
		return handleDateCollection(rest, client, store, now, "/v2/usercollection/daily_readiness")
	case "daily-sleep":
		return handleDateCollection(rest, client, store, now, "/v2/usercollection/daily_sleep")
	case "daily-stress":
		return handleDateCollection(rest, client, store, now, "/v2/usercollection/daily_stress")
	case "daily-spo2":
		return handleDateCollection(rest, client, store, now, "/v2/usercollection/daily_spo2")
	case "sleep":
		return handleDateCollection(rest, client, store, now, "/v2/usercollection/sleep")
	case "heartrate", "heart-rate":
		return handleDatetimeCollection(rest, client, store, now, "/v2/usercollection/heartrate")
	case "get":
		return handleGet(rest, client, store)
	case "config":
		return handleConfig(rest, store)
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

func parseGlobalArgs(args []string) (cliOptions, []string, error) {
	var opts cliOptions
	clean := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--pretty":
			opts.Pretty = true
		case "--output":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--output requires json or pretty")
				}
				value = args[i]
			}
			switch value {
			case "json", "compact":
				opts.Pretty = false
			case "pretty":
				opts.Pretty = true
			default:
				return opts, nil, fmt.Errorf("--output must be json or pretty, got %q", value)
			}
		case "--select":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--select requires a comma-separated field list")
				}
				value = args[i]
			}
			for _, field := range strings.Split(value, ",") {
				field = strings.TrimSpace(field)
				if field != "" {
					opts.Select = append(opts.Select, field)
				}
			}
		default:
			clean = append(clean, arg)
		}
	}
	return opts, clean, nil
}

func getAuthorized(client OuraGetter, store ConfigStore, path string, params map[string]string) (any, int, error) {
	config, err := store.Load()
	if err != nil {
		return nil, 1, err
	}
	token := effectiveAccessToken(config)
	if token == "" {
		return nil, 1, errors.New("missing Oura token; set OURA_TOKEN or run `oura config set-token <token>`")
	}
	payload, err := client.Get(context.Background(), path, params, token)
	if err != nil {
		var apiErr APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
			return nil, 2, codedError{code: 2, err: err}
		}
		return nil, 1, err
	}
	return payload, 0, nil
}

func handleDoctor(client OuraGetter, store ConfigStore) (any, int, error) {
	config, err := store.Load()
	if err != nil {
		return nil, 1, err
	}
	envTokenSet := strings.TrimSpace(os.Getenv("OURA_TOKEN")) != ""
	configTokenSet := strings.TrimSpace(config.AccessToken) != ""
	token := effectiveAccessToken(config)
	out := anyMap{
		"ok": false,
		"config": anyMap{
			"path":   store.Path(),
			"exists": store.Exists(),
		},
		"auth": anyMap{
			"env_token_set":    envTokenSet,
			"config_token_set": configTokenSet,
			"effective_source": tokenSource(envTokenSet, configTokenSet),
		},
		"api": anyMap{"checked": false},
	}
	if token == "" {
		out["hint"] = "Set OURA_TOKEN or run `oura config set-token <personal-access-token>`"
		return out, 0, nil
	}
	_, err = client.Get(context.Background(), "/v2/usercollection/personal_info", nil, token)
	api := anyMap{"checked": true, "ok": err == nil}
	if err != nil {
		api["error"] = err.Error()
		var apiErr APIError
		if errors.As(err, &apiErr) {
			api["status_code"] = apiErr.StatusCode
			if apiErr.StatusCode == http.StatusUnauthorized {
				api["hint"] = "Oura rejected the token; create a fresh token at https://cloud.ouraring.com/personal-access-tokens"
			}
		}
		out["api"] = api
		return out, 0, nil
	}
	out["ok"] = true
	out["api"] = api
	return out, 0, nil
}

func tokenSource(envTokenSet, configTokenSet bool) string {
	switch {
	case envTokenSet:
		return "env:OURA_TOKEN"
	case configTokenSet:
		return "config"
	default:
		return "none"
	}
}

func handleDateCollection(args []string, client OuraGetter, store ConfigStore, now func() time.Time, path string) (any, int, error) {
	opts, positionals, err := parseDateArgs(args)
	if err != nil {
		return nil, 1, err
	}
	if len(positionals) != 0 {
		return nil, 1, errors.New("unexpected positional argument")
	}
	params, err := dateParams(opts, now())
	if err != nil {
		return nil, 1, err
	}
	payload, code, err := getAuthorized(client, store, path, params)
	if err != nil || opts.Raw {
		return payload, code, err
	}
	return summarizeOuraPayload(path, params, payload), code, nil
}

func handleDatetimeCollection(args []string, client OuraGetter, store ConfigStore, now func() time.Time, path string) (any, int, error) {
	opts, positionals, err := parseDatetimeArgs(args)
	if err != nil {
		return nil, 1, err
	}
	if len(positionals) != 0 {
		return nil, 1, errors.New("unexpected positional argument")
	}
	params, err := datetimeParams(opts, now())
	if err != nil {
		return nil, 1, err
	}
	payload, code, err := getAuthorized(client, store, path, params)
	if err != nil || opts.Raw {
		return payload, code, err
	}
	return summarizeOuraPayload(path, params, payload), code, nil
}

func handleGet(args []string, client OuraGetter, store ConfigStore) (any, int, error) {
	if len(args) < 1 {
		return nil, 1, errors.New("usage: oura get <path> [--param key=value]")
	}
	path := normalizeAPIPath(args[0])
	params := map[string]string{}
	raw := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		if name == "--raw" {
			raw = true
			continue
		}
		if name != "--param" {
			return nil, 1, fmt.Errorf("unknown flag %q", arg)
		}
		if !hasValue {
			i++
			if i >= len(args) {
				return nil, 1, errors.New("--param requires key=value")
			}
			value = args[i]
		}
		key, val, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, 1, errors.New("--param requires key=value")
		}
		params[key] = val
	}
	payload, code, err := getAuthorized(client, store, path, params)
	if err != nil || raw {
		return payload, code, err
	}
	return summarizeOuraPayload(path, params, payload), code, nil
}

func normalizeAPIPath(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		u, err := url.Parse(path)
		if err == nil && u.Path != "" {
			return u.Path
		}
	}
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "/") {
		return path
	}
	if strings.HasPrefix(path, "v2/") {
		return "/" + path
	}
	return "/v2/usercollection/" + path
}

func handleConfig(args []string, store ConfigStore) (any, int, error) {
	if len(args) < 1 {
		return nil, 1, errors.New("usage: oura config <init|show|set-token|clear-token>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "init":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: oura config init")
		}
		created, err := store.Init()
		if err != nil {
			return nil, 1, err
		}
		return anyMap{"path": store.Path(), "created": created}, 0, nil
	case "show":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: oura config show")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		payload := config.ToMap()
		payload["path"] = store.Path()
		payload["exists"] = store.Exists()
		payload["env_token_set"] = os.Getenv("OURA_TOKEN") != ""
		return payload, 0, nil
	case "set-token":
		if len(rest) != 1 {
			return nil, 1, errors.New("usage: oura config set-token <token>")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		config.AccessToken = strings.TrimSpace(rest[0])
		if config.AccessToken == "" {
			return nil, 1, errors.New("token cannot be empty")
		}
		if err := store.Save(config); err != nil {
			return nil, 1, err
		}
		return anyMap{"access_token": redactedToken, "path": store.Path()}, 0, nil
	case "clear-token":
		if len(rest) != 0 {
			return nil, 1, errors.New("usage: oura config clear-token")
		}
		config, err := store.Load()
		if err != nil {
			return nil, 1, err
		}
		config.AccessToken = ""
		if err := store.Save(config); err != nil {
			return nil, 1, err
		}
		return anyMap{"access_token": nil, "path": store.Path()}, 0, nil
	default:
		return nil, 1, fmt.Errorf("unknown config command %q", sub)
	}
}

type dateOptions struct {
	StartDate string
	EndDate   string
	Days      int
	Raw       bool
}

func parseDateArgs(args []string) (dateOptions, []string, error) {
	opts := dateOptions{Days: 7}
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--start-date":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--start-date requires a value")
				}
				value = args[i]
			}
			opts.StartDate = value
		case "--end-date":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--end-date requires a value")
				}
				value = args[i]
			}
			opts.EndDate = value
		case "--days":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--days requires a value")
				}
				value = args[i]
			}
			days, err := strconv.Atoi(value)
			if err != nil || days <= 0 {
				return opts, nil, errors.New("--days must be a positive integer")
			}
			opts.Days = days
		case "--raw":
			opts.Raw = true
		default:
			if strings.HasPrefix(arg, "--") {
				return opts, nil, fmt.Errorf("unknown flag %q", arg)
			}
			pos = append(pos, arg)
		}
	}
	return opts, pos, nil
}

func dateParams(opts dateOptions, now time.Time) (map[string]string, error) {
	endDate := strings.TrimSpace(opts.EndDate)
	var end time.Time
	var err error
	if endDate == "" {
		end = now.In(time.Local)
		endDate = end.Format("2006-01-02")
	} else {
		end, err = parseDate(endDate)
		if err != nil {
			return nil, err
		}
	}

	startDate := strings.TrimSpace(opts.StartDate)
	if startDate == "" {
		if opts.Days <= 0 {
			opts.Days = 7
		}
		startDate = end.AddDate(0, 0, -(opts.Days - 1)).Format("2006-01-02")
	} else if _, err := parseDate(startDate); err != nil {
		return nil, err
	}
	if startDate > endDate {
		return nil, errors.New("start date must be on or before end date")
	}
	return map[string]string{"start_date": startDate, "end_date": endDate}, nil
}

func parseDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("date must use YYYY-MM-DD: %q", value)
	}
	return parsed, nil
}

type datetimeOptions struct {
	StartDateTime string
	EndDateTime   string
	Raw           bool
}

func parseDatetimeArgs(args []string) (datetimeOptions, []string, error) {
	var opts datetimeOptions
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--start-datetime":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--start-datetime requires a value")
				}
				value = args[i]
			}
			opts.StartDateTime = value
		case "--end-datetime":
			if !hasValue {
				i++
				if i >= len(args) {
					return opts, nil, errors.New("--end-datetime requires a value")
				}
				value = args[i]
			}
			opts.EndDateTime = value
		case "--raw":
			opts.Raw = true
		default:
			if strings.HasPrefix(arg, "--") {
				return opts, nil, fmt.Errorf("unknown flag %q", arg)
			}
			pos = append(pos, arg)
		}
	}
	return opts, pos, nil
}

func datetimeParams(opts datetimeOptions, now time.Time) (map[string]string, error) {
	end := now.UTC()
	var err error
	if opts.EndDateTime != "" {
		end, err = time.Parse(time.RFC3339, opts.EndDateTime)
		if err != nil {
			return nil, fmt.Errorf("end datetime must use RFC3339: %q", opts.EndDateTime)
		}
	}
	start := end.Add(-24 * time.Hour)
	if opts.StartDateTime != "" {
		start, err = time.Parse(time.RFC3339, opts.StartDateTime)
		if err != nil {
			return nil, fmt.Errorf("start datetime must use RFC3339: %q", opts.StartDateTime)
		}
	}
	if start.After(end) {
		return nil, errors.New("start datetime must be on or before end datetime")
	}
	return map[string]string{"start_datetime": start.UTC().Format(time.RFC3339), "end_datetime": end.UTC().Format(time.RFC3339)}, nil
}

func summarizeOuraPayload(path string, params map[string]string, payload any) any {
	data, nextToken, ok := collectionData(payload)
	if !ok {
		return payload
	}

	endpoint := collectionName(path)
	out := anyMap{"endpoint": endpoint, "count": len(data)}
	if len(params) != 0 {
		out["params"] = params
	}
	if nextToken != nil {
		out["next_token"] = nextToken
	}

	switch endpoint {
	case "sleep":
		out["raw_omitted"] = "id,ring,algo,heart_rate,hrv,movement_30_sec,sleep_phase_*; use --raw"
		out["data"] = mapItems(data, summarizeSleepPeriod)
	case "daily_activity":
		out["raw_omitted"] = "id,timestamp,met,class_5_min; use --raw"
		out["data"] = mapItems(data, summarizeDailyActivity)
	case "heartrate":
		return summarizeHeartRate(endpoint, params, data, nextToken)
	case "daily_sleep":
		out["raw_omitted"] = "id,timestamp; use --raw"
		out["data"] = mapItems(data, summarizeDailySleep)
	case "daily_readiness":
		out["raw_omitted"] = "id,timestamp; use --raw"
		out["data"] = mapItems(data, summarizeDailyReadiness)
	case "daily_spo2":
		out["raw_omitted"] = "id; use --raw"
		out["data"] = mapItems(data, summarizeDailySpO2)
	case "daily_stress":
		out["raw_omitted"] = "id; use --raw"
		out["data"] = mapItems(data, summarizeDailyStress)
	default:
		out["data"] = data
	}
	return out
}

func collectionData(payload any) ([]any, any, bool) {
	m, ok := asAnyMap(payload)
	if !ok {
		return nil, nil, false
	}
	rawData, ok := m["data"]
	if !ok {
		return nil, nil, false
	}
	data, ok := rawData.([]any)
	if !ok {
		return nil, nil, false
	}
	return data, m["next_token"], true
}

func collectionName(path string) string {
	path = normalizeAPIPath(path)
	return strings.TrimPrefix(path, "/v2/usercollection/")
}

func mapItems(data []any, summarize func(anyMap) anyMap) []any {
	items := make([]any, 0, len(data))
	for _, raw := range data {
		m, ok := asAnyMap(raw)
		if !ok {
			items = append(items, raw)
			continue
		}
		items = append(items, summarize(m))
	}
	return items
}

func summarizeSleepPeriod(m anyMap) anyMap {
	out := anyMap{}
	copyFields(out, m, "day", "type", "bedtime_start", "bedtime_end", "efficiency", "average_heart_rate", "lowest_heart_rate", "average_hrv", "average_breath", "restless_periods", "sleep_score_delta", "readiness_score_delta")
	copyDurationMinutes(out, m, "total_sleep_min", "total_sleep_duration")
	copyDurationMinutes(out, m, "time_in_bed_min", "time_in_bed")
	copyDurationMinutes(out, m, "awake_min", "awake_time")
	copyDurationMinutes(out, m, "rem_min", "rem_sleep_duration")
	copyDurationMinutes(out, m, "deep_min", "deep_sleep_duration")
	copyDurationMinutes(out, m, "light_min", "light_sleep_duration")
	copyDurationMinutes(out, m, "latency_min", "latency")
	if readiness, ok := asAnyMap(m["readiness"]); ok {
		copyFieldAs(out, readiness, "readiness_score", "score")
		copyFieldAs(out, readiness, "temperature_deviation", "temperature_deviation")
	}
	return out
}

func summarizeDailyActivity(m anyMap) anyMap {
	out := anyMap{}
	copyFields(out, m, "day", "score", "steps", "active_calories", "total_calories", "target_calories", "equivalent_walking_distance", "meters_to_target", "target_meters", "average_met_minutes", "inactivity_alerts")
	copyDurationMinutes(out, m, "high_activity_min", "high_activity_time")
	copyDurationMinutes(out, m, "medium_activity_min", "medium_activity_time")
	copyDurationMinutes(out, m, "low_activity_min", "low_activity_time")
	copyDurationMinutes(out, m, "sedentary_min", "sedentary_time")
	copyDurationMinutes(out, m, "resting_min", "resting_time")
	copyDurationMinutes(out, m, "non_wear_min", "non_wear_time")
	copyField(out, m, "contributors")
	return out
}

func summarizeDailySleep(m anyMap) anyMap {
	out := anyMap{}
	copyFields(out, m, "day", "score", "contributors")
	return out
}

func summarizeDailyReadiness(m anyMap) anyMap {
	out := anyMap{}
	copyFields(out, m, "day", "score", "temperature_deviation", "temperature_trend_deviation", "contributors")
	return out
}

func summarizeDailySpO2(m anyMap) anyMap {
	out := anyMap{}
	copyFields(out, m, "day", "spo2_percentage", "breathing_disturbance_index")
	return out
}

func summarizeDailyStress(m anyMap) anyMap {
	out := anyMap{}
	copyFields(out, m, "day", "stress_high", "recovery_high", "day_summary")
	return out
}

func summarizeHeartRate(endpoint string, params map[string]string, data []any, nextToken any) anyMap {
	bucketsByHour := map[time.Time]*heartRateBucket{}
	var first, last time.Time
	sampleCount := 0
	for _, raw := range data {
		m, ok := asAnyMap(raw)
		if !ok {
			continue
		}
		bpm, ok := numberAsFloat(m["bpm"])
		if !ok {
			continue
		}
		timestamp, ok := stringValue(m["timestamp"])
		if !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			continue
		}
		t = t.UTC()
		if sampleCount == 0 || t.Before(first) {
			first = t
		}
		if sampleCount == 0 || t.After(last) {
			last = t
		}
		sampleCount++
		hour := t.Truncate(time.Hour)
		bucket := bucketsByHour[hour]
		if bucket == nil {
			bucket = &heartRateBucket{Min: bpm, Max: bpm}
			bucketsByHour[hour] = bucket
		}
		bucket.Count++
		bucket.Sum += bpm
		if bpm < bucket.Min {
			bucket.Min = bpm
		}
		if bpm > bucket.Max {
			bucket.Max = bpm
		}
	}

	hours := make([]time.Time, 0, len(bucketsByHour))
	for hour := range bucketsByHour {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	buckets := make([]any, 0, len(hours))
	for _, hour := range hours {
		bucket := bucketsByHour[hour]
		buckets = append(buckets, anyMap{
			"hour":    hour.Format(time.RFC3339),
			"samples": bucket.Count,
			"avg_bpm": round1(bucket.Sum / float64(bucket.Count)),
			"min_bpm": round1(bucket.Min),
			"max_bpm": round1(bucket.Max),
		})
	}

	out := anyMap{"endpoint": endpoint, "sample_count": sampleCount, "bucket_minutes": 60, "data": buckets, "raw_omitted": "samples,producer_timestamp; use --raw"}
	if len(params) != 0 {
		out["params"] = params
	}
	if sampleCount != 0 {
		out["start_datetime"] = first.Format(time.RFC3339)
		out["end_datetime"] = last.Format(time.RFC3339)
	}
	if nextToken != nil {
		out["next_token"] = nextToken
	}
	return out
}

type heartRateBucket struct {
	Count int
	Sum   float64
	Min   float64
	Max   float64
}

func asAnyMap(value any) (anyMap, bool) {
	switch v := value.(type) {
	case anyMap:
		return v, true
	case map[string]any:
		return anyMap(v), true
	default:
		return nil, false
	}
}

func copyFields(dst, src anyMap, fields ...string) {
	for _, field := range fields {
		copyField(dst, src, field)
	}
}

func copyField(dst, src anyMap, field string) {
	copyFieldAs(dst, src, field, field)
}

func copyFieldAs(dst, src anyMap, outKey, inKey string) {
	value, ok := src[inKey]
	if !ok || value == nil {
		return
	}
	dst[outKey] = value
}

func copyDurationMinutes(dst, src anyMap, outKey, inKey string) {
	seconds, ok := numberAsFloat(src[inKey])
	if !ok {
		return
	}
	dst[outKey] = int(math.Round(seconds / 60))
}

func numberAsFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint64:
		return float64(v), true
	case uint32:
		return float64(v), true
	default:
		return 0, false
	}
}

func stringValue(value any) (string, bool) {
	v, ok := value.(string)
	return v, ok && v != ""
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

func selectPayloadFields(payload any, fields []string) any {
	var out any = anyMap{}
	matched := false
	for _, field := range fields {
		parts := splitSelectField(field)
		if len(parts) == 0 {
			continue
		}
		projected, ok := projectField(payload, parts)
		if !ok {
			continue
		}
		out = mergeProjected(out, projected)
		matched = true
	}
	if !matched {
		return anyMap{"selected": fields, "data": []any{}}
	}
	return out
}

func splitSelectField(field string) []string {
	rawParts := strings.Split(field, ".")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func projectField(value any, parts []string) (any, bool) {
	if len(parts) == 0 {
		return value, true
	}
	if m, ok := asAnyMap(value); ok {
		child, exists := m[parts[0]]
		if !exists {
			return nil, false
		}
		projected, ok := projectField(child, parts[1:])
		if !ok {
			return nil, false
		}
		return anyMap{parts[0]: projected}, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]any, len(items))
	matched := false
	for i, item := range items {
		projected, ok := projectField(item, parts)
		if ok {
			out[i] = projected
			matched = true
		} else {
			out[i] = anyMap{}
		}
	}
	return out, matched
}

func mergeProjected(dst, src any) any {
	dstMap, dstIsMap := asAnyMap(dst)
	srcMap, srcIsMap := asAnyMap(src)
	if dstIsMap && srcIsMap {
		for key, srcValue := range srcMap {
			if dstValue, ok := dstMap[key]; ok {
				dstMap[key] = mergeProjected(dstValue, srcValue)
			} else {
				dstMap[key] = srcValue
			}
		}
		return dstMap
	}
	dstItems, dstIsArray := dst.([]any)
	srcItems, srcIsArray := src.([]any)
	if dstIsArray && srcIsArray {
		if len(srcItems) > len(dstItems) {
			extended := make([]any, len(srcItems))
			copy(extended, dstItems)
			dstItems = extended
		}
		for i, srcValue := range srcItems {
			if dstItems[i] == nil {
				dstItems[i] = srcValue
			} else {
				dstItems[i] = mergeProjected(dstItems[i], srcValue)
			}
		}
		return dstItems
	}
	return src
}

func printJSON(v any, pretty bool) {
	enc := json.NewEncoder(os.Stdout)
	if pretty {
		enc.SetIndent("", "  ")
	}
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

type OuraConfig struct {
	AccessToken string
}

func (c OuraConfig) ToMap() anyMap {
	return anyMap{"access_token": redactedOrNil(c.AccessToken)}
}

func effectiveAccessToken(config OuraConfig) string {
	if token := strings.TrimSpace(os.Getenv("OURA_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(config.AccessToken)
}

type ConfigStore interface {
	Path() string
	Exists() bool
	Load() (OuraConfig, error)
	Init() (bool, error)
	Save(OuraConfig) error
}

type FileConfigStore struct{ path string }

func NewConfigStore(path string) FileConfigStore { return FileConfigStore{path: path} }
func (s FileConfigStore) Path() string           { return s.path }
func (s FileConfigStore) Exists() bool           { _, err := os.Stat(s.path); return err == nil }

func (s FileConfigStore) Load() (OuraConfig, error) {
	config := OuraConfig{}
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
		case "access_token":
			config.AccessToken = val
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
	return true, s.Save(OuraConfig{})
}

func (s FileConfigStore) Save(config OuraConfig) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Defaults for the oura CLI.\n")
	if strings.TrimSpace(config.AccessToken) != "" {
		b.WriteString(fmt.Sprintf("access_token = %q\n", strings.TrimSpace(config.AccessToken)))
	}
	return os.WriteFile(s.path, []byte(b.String()), 0o600)
}

func defaultConfigPath() string {
	if path := os.Getenv("OURA_CONFIG"); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "oura", "config.toml")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "oura", "config.toml")
	}
	return "config.toml"
}

type OuraGetter interface {
	Get(ctx context.Context, path string, params map[string]string, token string) (any, error)
}

type OuraClient struct {
	baseURL string
	http    *http.Client
}

func NewOuraClient(baseURL string) OuraClient {
	return OuraClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 30 * time.Second}}
}

type APIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("Oura API request failed: %s", e.Status)
	}
	return fmt.Sprintf("Oura API request failed: %s: %s", e.Status, body)
}

func (c OuraClient) Get(ctx context.Context, path string, params map[string]string, token string) (any, error) {
	u, err := url.Parse(c.baseURL + normalizeAPIPath(path))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for key, value := range params {
		if value != "" {
			q.Set(key, value)
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "oura-ring-cli")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, APIError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(body)}
	}
	var payload any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func updateCLI(args []string) (any, error) {
	if len(args) > 1 {
		return nil, errors.New("usage: oura update [install_path]")
	}
	target, err := updateTarget(args)
	if err != nil {
		return nil, err
	}
	previousChecksum, _ := fileSHA256(target)
	source := os.Getenv("OURA_CLI_UPDATE_SOURCE")
	if source == "" {
		source = os.Getenv("OURA_CLI_UPDATE_URL")
	}
	if source == "" {
		source = defaultUpdateSource
	}
	tmp, err := os.MkdirTemp("", "oura-cli-update-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	checkout := filepath.Join(tmp, "src")
	if err := fetchSource(source, checkout); err != nil {
		return nil, err
	}
	cliDir := filepath.Join(checkout, "cli", "oura-ring-cli")
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
		return filepath.Join(home, ".local", "bin", "oura"), nil
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
	resp, err := httpDefaultClient().Get(source)
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

func redactedOrNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return redactedToken
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
