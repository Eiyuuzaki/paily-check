package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Sub-structures
// ---------------------------------------------------------------------------

type CoreConfig struct {
	URL    string `yaml:"url"`
	Secret string `yaml:"secret"`
}

type ScheduleConfig struct {
	Cron string `yaml:"cron"`
	// UrgentPollSeconds is how often the checker asks paily-core whether an
	// operator queued a recheck. 0 disables polling, leaving only the cron.
	UrgentPollSeconds int `yaml:"urgent_poll_seconds"`
}

// InitialConfig controls the initial latency pass over all nodes.
type InitialConfig struct {
	PingURL          string `yaml:"ping_url"`
	PingAvg          int    `yaml:"ping_avg"`
	PingTimeoutMS    int    `yaml:"ping_timeout_ms"`
	PingGapMS        int    `yaml:"ping_gap_ms"` // pause between samples; 0 disables
	Concurrency      int    `yaml:"concurrency"`
	AliveThresholdMS int    `yaml:"alive_threshold_ms"`
	ECHMaxCandidates int    `yaml:"ech_max_candidates"` // 0 disables ECH comparison
}

// DeepPingConfig controls detailed latency checks.
type DeepPingConfig struct {
	Concurrency    int    `yaml:"concurrency"`
	Avg            int    `yaml:"avg"`
	PingURL        string `yaml:"ping_url"`
	TimeoutMS      int    `yaml:"timeout_ms"`
	GapMS          int    `yaml:"gap_ms"`           // pause between samples; 0 disables
	FilterDeadDeep bool   `yaml:"filter_dead_deep"` // drop nodes whose deep ping latency is -1
}

// GeoConfig controls geo/region detection.
type GeoConfig struct {
	Concurrency     int                `yaml:"concurrency"`
	Mode            string             `yaml:"mode"`      // "script" | "mmdb" | "native_api"
	MmdbPath        string             `yaml:"mmdb_path"` // required when mode=mmdb
	FallbackLegacy  bool               `yaml:"fallback_legacy"`
	FallbackInbound bool               `yaml:"fallback_inbound"`
	IpScript        string             `yaml:"ip_script"`  // optional custom IP-lookup script
	GeoScript       string             `yaml:"geo_script"` // optional custom GeoIP script
	Retry           int                `yaml:"retry"`
	NativeAPI       GeoNativeAPIConfig `yaml:"native_api"`
}

type GeoNativeAPIConfig struct {
	TimeoutMS int      `yaml:"timeout_ms"`
	Providers []string `yaml:"providers"`
}

// ScriptConfig controls third-party script checks.
type ScriptConfig struct {
	NodeConcurrency   int      `yaml:"node_concurrency"`
	ScriptConcurrency int      `yaml:"script_concurrency"`
	Dir               string   `yaml:"dir"`
	TimeoutMS         int      `yaml:"timeout_ms"`
	Engine            string   `yaml:"engine"`
	Regions           []string `yaml:"regions"`
	Scripts           []string `yaml:"scripts"`
}

// SpeedConfig controls the optional download speed test.
type SpeedConfig struct {
	Concurrency int    `yaml:"concurrency"`
	Enabled     bool   `yaml:"enabled"`
	URL         string `yaml:"url"`
	Threads     int    `yaml:"threads"`
	DurationS   int    `yaml:"duration_s"`
}

// ProgressBarConfig enables the live terminal progress indicator.
type ProgressBarConfig struct {
	Enabled bool `yaml:"enabled"`
}

// DeepConfig groups detailed-check configuration.
type DeepConfig struct {
	Ping   DeepPingConfig `yaml:"ping"`
	Geo    GeoConfig      `yaml:"geo"`
	Script ScriptConfig   `yaml:"script"`
	Speed  SpeedConfig    `yaml:"speed"`
}

// Config is the root configuration object.
type Config struct {
	Core        CoreConfig        `yaml:"core"`
	Schedule    ScheduleConfig    `yaml:"schedule"`
	Initial     InitialConfig     `yaml:"initial"`
	Deep        DeepConfig        `yaml:"deep"`
	ProgressBar ProgressBarConfig `yaml:"progress_bar"`
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

func defaults() *Config {
	return &Config{
		Core: CoreConfig{
			URL:    "http://localhost:8080",
			Secret: "",
		},
		Schedule: ScheduleConfig{
			Cron: "0 */30 * * * *",
			// One minute: an operator enqueue is acted on almost immediately,
			// and the request is a single row read on the core side.
			UrgentPollSeconds: 60,
		},
		Initial: InitialConfig{
			PingURL:          "https://www.gstatic.com/generate_204",
			PingAvg:          3,
			PingTimeoutMS:    3000,
			PingGapMS:        1000,
			Concurrency:      50,
			AliveThresholdMS: 3000,
			ECHMaxCandidates: 40,
		},
		Deep: DeepConfig{
			Ping: DeepPingConfig{
				Concurrency: 20,
				Avg:         7,
				PingURL:     "https://www.gstatic.com/generate_204",
				TimeoutMS:   3000,
				GapMS:       1000,
			},
			Geo: GeoConfig{
				Concurrency:     20,
				Mode:            "script",
				FallbackLegacy:  true,
				FallbackInbound: true,
				Retry:           2,
				NativeAPI: GeoNativeAPIConfig{
					TimeoutMS: 5000,
					Providers: []string{
						"cloudflare-trace",
						"ipwhois",
						"myip",
						"ipapi-co",
						"ident-me",
						"ip-api",
						"ip-sb",
						"ipinfo",
					},
				},
			},
			Script: ScriptConfig{
				NodeConcurrency:   5,
				ScriptConcurrency: 32,
				Dir:               "./scripts",
				TimeoutMS:         10000,
				Engine:            "goja",
				Scripts:           nil,
			},
			Speed: SpeedConfig{
				Concurrency: 2,
				Enabled:     false,
				Threads:     4,
				DurationS:   8,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

// Load reads YAML from path (if non-empty), then overlays environment
// variables. Environment variables always take precedence over file values.
// If path is empty the defaults are returned with env overrides applied.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse yaml: %w", err)
		}
	}

	applyEnv(cfg)
	warnMissingSecret(cfg)
	warnUnreachableRescue(cfg)
	return cfg, nil
}

// warnMissingSecret reports the one setting without which nothing works: every
// call to paily-core carries this as a Bearer token, so an empty value means the
// process starts, schedules rounds, and has every request rejected. Failing at
// startup would point at the cause immediately; a warning is the compromise,
// since a local run against a token-less core is legitimate.
func warnMissingSecret(cfg *Config) {
	if strings.TrimSpace(cfg.Core.Secret) == "" {
		log.Printf("[config] warning: no service secret configured (PAILY_SERVICE_SECRET); paily-core will reject every request with 401")
	}
}

// warnUnreachableRescue reports a configuration in which the slow-node rescue
// can never fire.
//
// initial.RunAll gives a node whose every sample timed out one more attempt with
// a doubled budget, so a slow host is not classified dead by a hiccup. The
// result is compared against Initial.AliveThresholdMS like any other latency, so
// the rescue only helps a node whose true latency L satisfies
// PingTimeoutMS < L <= AliveThresholdMS. When the threshold is not above the
// timeout that range is empty and the rescue is dead code: with the shipped
// defaults (both 3000ms) it can never report anything the normal path would not
// have. Silent, because a wrong threshold changes no output — it only removes a
// safety net.
func warnUnreachableRescue(cfg *Config) {
	if cfg.Initial.AliveThresholdMS <= cfg.Initial.PingTimeoutMS {
		log.Printf("[config] warning: initial.alive_threshold_ms (%d) is not above initial.ping_timeout_ms (%d); the slow-node rescue cannot fire",
			cfg.Initial.AliveThresholdMS, cfg.Initial.PingTimeoutMS)
	}
}

// ---------------------------------------------------------------------------
// Environment variable overrides
// ---------------------------------------------------------------------------

func applyEnv(cfg *Config) {
	// Core
	envStr("PAILY_CORE_URL", &cfg.Core.URL)
	envStr("PAILY_SERVICE_SECRET", &cfg.Core.Secret)

	// Schedule
	envStr("CHECK_CRON", &cfg.Schedule.Cron)
	envInt("CHECK_URGENT_POLL_SECONDS", &cfg.Schedule.UrgentPollSeconds)

	// Initial
	envStr("INITIAL_PING_URL", &cfg.Initial.PingURL)
	envInt("INITIAL_PING_AVG", &cfg.Initial.PingAvg)
	envInt("INITIAL_PING_TIMEOUT_MS", &cfg.Initial.PingTimeoutMS)
	envInt("INITIAL_PING_GAP_MS", &cfg.Initial.PingGapMS)
	envInt("INITIAL_CONCURRENCY", &cfg.Initial.Concurrency)
	envInt("INITIAL_ALIVE_THRESHOLD_MS", &cfg.Initial.AliveThresholdMS)
	envInt("INITIAL_ECH_MAX_CANDIDATES", &cfg.Initial.ECHMaxCandidates)

	// Deep ping
	envInt("DEEP_PING_CONCURRENCY", &cfg.Deep.Ping.Concurrency)
	envInt("DEEP_PING_AVG", &cfg.Deep.Ping.Avg)
	envStr("DEEP_PING_URL", &cfg.Deep.Ping.PingURL)
	envInt("DEEP_PING_TIMEOUT_MS", &cfg.Deep.Ping.TimeoutMS)
	envInt("DEEP_PING_GAP_MS", &cfg.Deep.Ping.GapMS)
	envBool("DEEP_PING_FILTER_DEAD", &cfg.Deep.Ping.FilterDeadDeep)

	// Deep geo
	envInt("DEEP_GEO_CONCURRENCY", &cfg.Deep.Geo.Concurrency)
	envStr("GEO_MODE", &cfg.Deep.Geo.Mode)
	envStr("GEO_MMDB_PATH", &cfg.Deep.Geo.MmdbPath)
	envBool("GEO_FALLBACK_LEGACY", &cfg.Deep.Geo.FallbackLegacy)
	envBool("GEO_FALLBACK_INBOUND", &cfg.Deep.Geo.FallbackInbound)
	envStr("GEO_IP_SCRIPT", &cfg.Deep.Geo.IpScript)
	envStr("GEO_GEO_SCRIPT", &cfg.Deep.Geo.GeoScript)
	envInt("GEO_RETRY", &cfg.Deep.Geo.Retry)
	envInt("GEO_NATIVE_API_TIMEOUT_MS", &cfg.Deep.Geo.NativeAPI.TimeoutMS)
	envCSV("GEO_NATIVE_API_PROVIDERS", &cfg.Deep.Geo.NativeAPI.Providers)

	// Deep script
	envInt("DEEP_SCRIPT_NODE_CONCURRENCY", &cfg.Deep.Script.NodeConcurrency)
	envInt("DEEP_SCRIPT_CONCURRENCY", &cfg.Deep.Script.ScriptConcurrency)
	envStr("SCRIPT_DIR", &cfg.Deep.Script.Dir)
	envInt("SCRIPT_TIMEOUT_MS", &cfg.Deep.Script.TimeoutMS)
	envStr("SCRIPT_ENGINE", &cfg.Deep.Script.Engine)
	envCSV("SCRIPT_REGIONS", &cfg.Deep.Script.Regions)
	envCSV("SCRIPT_LIST", &cfg.Deep.Script.Scripts)

	// Deep speed
	envInt("DEEP_SPEED_CONCURRENCY", &cfg.Deep.Speed.Concurrency)
	envBool("SPEED_ENABLED", &cfg.Deep.Speed.Enabled)
	envStr("SPEED_URL", &cfg.Deep.Speed.URL)
	envInt("SPEED_THREADS", &cfg.Deep.Speed.Threads)
	envInt("SPEED_DURATION_S", &cfg.Deep.Speed.DurationS)

	// Progress bar
	envBool("PROGRESS_BAR", &cfg.ProgressBar.Enabled)
}

// helpers

func envStr(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// envInt, envBool and envCSV report a value they could not understand instead
// of leaving the previous one in place without a word. An override that silently
// does not apply is the same trap this project already hit with a name template
// that existed in the database and quietly overrode the code: the operator
// believes the setting took effect, and nothing in the output says otherwise.
func envInt(key string, dst *int) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		log.Printf("[config] warning: %s=%q is not an integer; keeping %d", key, v, *dst)
		return
	}
	*dst = n
}

func envBool(key string, dst *bool) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	// Case-insensitive: SPEED_ENABLED=True used to be ignored outright, which is
	// an easy thing to write and impossible to notice.
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		*dst = true
	case "false", "0", "no":
		*dst = false
	default:
		log.Printf("[config] warning: %s=%q is not a boolean; keeping %t", key, v, *dst)
	}
}

func envCSV(key string, dst *[]string) {
	if v := os.Getenv(key); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
		*dst = out
	}
}
