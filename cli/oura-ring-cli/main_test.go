package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeOuraClient struct {
	path    string
	params  map[string]string
	token   string
	payload any
	err     error
}

func (c *fakeOuraClient) Get(ctx context.Context, path string, params map[string]string, token string) (any, error) {
	c.path = path
	c.params = map[string]string{}
	for key, value := range params {
		c.params[key] = value
	}
	c.token = token
	if c.err != nil {
		return nil, c.err
	}
	if c.payload != nil {
		return c.payload, nil
	}
	return anyMap{"ok": true}, nil
}

func runTestCLI(t *testing.T, args []string, client *fakeOuraClient, store ConfigStore) (any, int, error) {
	t.Helper()
	payload, code, err := runCLI(args, client, store, func() time.Time {
		return time.Date(2026, 5, 6, 15, 4, 5, 0, time.UTC)
	})
	if err == nil {
		if _, err := json.Marshal(payload); err != nil {
			t.Fatalf("payload is not JSON-serializable: %v", err)
		}
	}
	return payload, code, err
}

func testStore(t *testing.T) FileConfigStore {
	t.Helper()
	return NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))
}

func TestConfigInitSetShowAndClearToken(t *testing.T) {
	t.Setenv("OURA_TOKEN", "")
	store := testStore(t)
	client := &fakeOuraClient{}

	payload, code, err := runTestCLI(t, []string{"config", "init"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("config init code=%d err=%v", code, err)
	}
	if payload.(anyMap)["created"] != true {
		t.Fatalf("expected created=true, got %#v", payload)
	}

	_, code, err = runTestCLI(t, []string{"config", "set-token", "secret-token"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("config set-token code=%d err=%v", code, err)
	}
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.AccessToken != "secret-token" {
		t.Fatalf("token was not saved: %#v", config)
	}

	payload, code, err = runTestCLI(t, []string{"config", "show"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("config show code=%d err=%v", code, err)
	}
	if got := payload.(anyMap)["access_token"]; got != redactedToken {
		t.Fatalf("access_token=%#v", got)
	}

	_, code, err = runTestCLI(t, []string{"config", "clear-token"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("config clear-token code=%d err=%v", code, err)
	}
	config, _ = store.Load()
	if config.AccessToken != "" {
		t.Fatalf("token was not cleared: %#v", config)
	}
}

func TestPersonalInfoUsesEnvToken(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{}

	_, code, err := runTestCLI(t, []string{"personal-info"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("personal-info code=%d err=%v", code, err)
	}
	if client.path != "/v2/usercollection/personal_info" || client.token != "env-token" {
		t.Fatalf("path/token = %q/%q", client.path, client.token)
	}
}

func TestDailyActivityDefaultsToSevenDayWindow(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{}

	_, code, err := runTestCLI(t, []string{"daily-activity"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("daily-activity code=%d err=%v", code, err)
	}
	expected := map[string]string{"start_date": "2026-04-30", "end_date": "2026-05-06"}
	if client.path != "/v2/usercollection/daily_activity" || !reflect.DeepEqual(client.params, expected) {
		t.Fatalf("path/params = %q/%#v", client.path, client.params)
	}
}

func TestDailySleepAcceptsDateFlags(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{}

	_, code, err := runTestCLI(t, []string{"daily-sleep", "--start-date", "2026-05-01", "--end-date=2026-05-03"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("daily-sleep code=%d err=%v", code, err)
	}
	expected := map[string]string{"start_date": "2026-05-01", "end_date": "2026-05-03"}
	if client.path != "/v2/usercollection/daily_sleep" || !reflect.DeepEqual(client.params, expected) {
		t.Fatalf("path/params = %q/%#v", client.path, client.params)
	}
}

func TestHeartrateDefaultsToLast24Hours(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{}

	_, code, err := runTestCLI(t, []string{"heartrate"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("heartrate code=%d err=%v", code, err)
	}
	expected := map[string]string{"start_datetime": "2026-05-05T15:04:05Z", "end_datetime": "2026-05-06T15:04:05Z"}
	if client.path != "/v2/usercollection/heartrate" || !reflect.DeepEqual(client.params, expected) {
		t.Fatalf("path/params = %q/%#v", client.path, client.params)
	}
}

func TestSleepSummaryOmitsHighFrequencyFields(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{payload: map[string]any{
		"data": []any{map[string]any{
			"id":                   "sleep-id",
			"day":                  "2026-05-01",
			"type":                 "long_sleep",
			"total_sleep_duration": 28800,
			"heart_rate":           map[string]any{"items": []any{50, 51, 52}},
			"hrv":                  map[string]any{"items": []any{90, 91, 92}},
			"movement_30_sec":      "1111111111",
			"sleep_phase_30_sec":   "2222222222",
		}},
	}}

	payload, code, err := runTestCLI(t, []string{"sleep", "--days", "7"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("sleep code=%d err=%v", code, err)
	}
	summary := payload.(anyMap)
	items := summary["data"].([]any)
	item := items[0].(anyMap)
	if item["total_sleep_min"] != 480 {
		t.Fatalf("total_sleep_min=%#v", item["total_sleep_min"])
	}
	for _, field := range []string{"id", "heart_rate", "hrv", "movement_30_sec", "sleep_phase_30_sec"} {
		if _, ok := item[field]; ok {
			t.Fatalf("summary should omit %q: %#v", field, item)
		}
	}
}

func TestRawFlagKeepsFullCollectionPayload(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	raw := map[string]any{"data": []any{map[string]any{"day": "2026-05-01", "heart_rate": []any{1, 2, 3}}}}
	client := &fakeOuraClient{payload: raw}

	payload, code, err := runTestCLI(t, []string{"sleep", "--raw"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("sleep --raw code=%d err=%v", code, err)
	}
	if !reflect.DeepEqual(payload, raw) {
		t.Fatalf("raw payload changed: %#v", payload)
	}
}

func TestHeartrateSummaryBucketsSamplesByHour(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{payload: map[string]any{
		"data": []any{
			map[string]any{"timestamp": "2026-05-01T01:00:00Z", "bpm": 50},
			map[string]any{"timestamp": "2026-05-01T01:30:00Z", "bpm": 70},
			map[string]any{"timestamp": "2026-05-01T02:00:00Z", "bpm": 80},
		},
	}}

	payload, code, err := runTestCLI(t, []string{"heartrate"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("heartrate code=%d err=%v", code, err)
	}
	summary := payload.(anyMap)
	if summary["sample_count"] != 3 {
		t.Fatalf("sample_count=%#v", summary["sample_count"])
	}
	buckets := summary["data"].([]any)
	if len(buckets) != 2 {
		t.Fatalf("buckets=%#v", buckets)
	}
	first := buckets[0].(anyMap)
	if first["samples"] != 2 || first["avg_bpm"] != 60.0 || first["min_bpm"] != 50.0 || first["max_bpm"] != 70.0 {
		t.Fatalf("first bucket=%#v", first)
	}
}

func TestSelectGlobalFlagProjectsNestedFields(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{payload: map[string]any{"data": []any{map[string]any{
		"day":                  "2026-05-01",
		"total_sleep_duration": 28800,
		"heart_rate":           []any{1, 2, 3},
	}}}}

	payload, code, err := runTestCLI(t, []string{"sleep", "--days", "7", "--select", "data.day,data.total_sleep_min"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("sleep --select code=%d err=%v", code, err)
	}
	selected := payload.(anyMap)
	items := selected["data"].([]any)
	item := items[0].(anyMap)
	if item["day"] != "2026-05-01" || item["total_sleep_min"] != 480 {
		t.Fatalf("selected item=%#v", item)
	}
	if _, ok := item["heart_rate"]; ok {
		t.Fatalf("select should omit heart_rate: %#v", item)
	}
}

func TestDoctorReportsConfiguredAuthWithoutLeakingProfile(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{payload: anyMap{"email": "private@example.com"}}

	payload, code, err := runTestCLI(t, []string{"doctor"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("doctor code=%d err=%v", code, err)
	}
	diag := payload.(anyMap)
	if diag["ok"] != true {
		t.Fatalf("doctor ok=%#v payload=%#v", diag["ok"], diag)
	}
	api := diag["api"].(anyMap)
	if api["ok"] != true || client.path != "/v2/usercollection/personal_info" {
		t.Fatalf("api=%#v path=%q", api, client.path)
	}
	if _, leaked := diag["email"]; leaked {
		t.Fatalf("doctor leaked profile payload: %#v", diag)
	}
}

func TestDoctorWithoutTokenReturnsHint(t *testing.T) {
	t.Setenv("OURA_TOKEN", "")
	store := testStore(t)
	client := &fakeOuraClient{}

	payload, code, err := runTestCLI(t, []string{"doctor"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("doctor code=%d err=%v", code, err)
	}
	diag := payload.(anyMap)
	if diag["ok"] != false || diag["hint"] == "" {
		t.Fatalf("doctor payload=%#v", diag)
	}
	if client.path != "" {
		t.Fatalf("API should not have been called, got path %q", client.path)
	}
}

func TestGenericGetNormalizesPathAndParams(t *testing.T) {
	t.Setenv("OURA_TOKEN", "env-token")
	store := testStore(t)
	client := &fakeOuraClient{}

	_, code, err := runTestCLI(t, []string{"get", "daily_activity", "--param", "start_date=2026-05-01", "--param=end_date=2026-05-02"}, client, store)
	if err != nil || code != 0 {
		t.Fatalf("get code=%d err=%v", code, err)
	}
	expected := map[string]string{"start_date": "2026-05-01", "end_date": "2026-05-02"}
	if client.path != "/v2/usercollection/daily_activity" || !reflect.DeepEqual(client.params, expected) {
		t.Fatalf("path/params = %q/%#v", client.path, client.params)
	}
}

func TestMissingTokenFailsBeforeAPIRequest(t *testing.T) {
	t.Setenv("OURA_TOKEN", "")
	store := testStore(t)
	client := &fakeOuraClient{}

	_, code, err := runTestCLI(t, []string{"personal-info"}, client, store)
	if err == nil || code != 1 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if client.path != "" {
		t.Fatalf("API should not have been called, got path %q", client.path)
	}
}

func TestUnauthorizedReturnsExitCode2(t *testing.T) {
	t.Setenv("OURA_TOKEN", "bad-token")
	store := testStore(t)
	client := &fakeOuraClient{err: APIError{StatusCode: 401, Status: "401 Unauthorized"}}

	_, code, err := runTestCLI(t, []string{"personal-info"}, client, store)
	if err == nil || code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	var coded codedError
	if !errors.As(err, &coded) {
		t.Fatalf("expected coded error, got %T", err)
	}
}
