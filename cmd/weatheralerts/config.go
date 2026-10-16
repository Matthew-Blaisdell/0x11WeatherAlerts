package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/subscriber"
)

// minInterval keeps us a polite API client: api.weather.gov caches alert
// responses for a few seconds, so polling faster than this gains nothing.
const minInterval = 30 * time.Second

type Config struct {
	// Contact goes into the User-Agent, which api.weather.gov requires so
	// NWS can reach you if your client misbehaves.
	Contact  string        `yaml:"contact"`
	Interval time.Duration `yaml:"interval"`
	Areas    []string      `yaml:"areas"`
	Database string        `yaml:"database"`
	Listen   string        `yaml:"listen"`
	// TimeZone for times shown in notifications, e.g. America/New_York.
	TimeZone string `yaml:"timezone"`
	// OpsTopic gets a message if polling fails for StaleAfter; defaults to the
	// first subscriber's topic. Set to "-" to disable.
	OpsTopic   string        `yaml:"ops_topic"`
	StaleAfter time.Duration `yaml:"stale_after"`
	Ntfy       struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"ntfy"`
	Subscribers []subscriber.Subscriber `yaml:"subscribers"`

	location *time.Location
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Interval:   time.Minute,
		Database:   "weatheralerts.db",
		StaleAfter: 15 * time.Minute,
		Listen:     ":8080",
	}
	cfg.Ntfy.URL = "https://ntfy.sh"
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if tok := os.Getenv("NTFY_TOKEN"); tok != "" {
		cfg.Ntfy.Token = tok
	}
	if cfg.TimeZone != "" {
		loc, err := time.LoadLocation(cfg.TimeZone)
		if err != nil {
			return nil, fmt.Errorf("timezone: %w", err)
		}
		cfg.location = loc
	}
	switch {
	case cfg.OpsTopic == "-":
		cfg.OpsTopic = ""
	case cfg.OpsTopic == "" && len(cfg.Subscribers) > 0:
		cfg.OpsTopic = cfg.Subscribers[0].Topic
	}
	return cfg, cfg.validate()
}

func (c *Config) validate() error {
	var errs []error
	if c.Contact == "" {
		errs = append(errs, errors.New("contact is required (api.weather.gov User-Agent)"))
	}
	if c.Interval < minInterval {
		errs = append(errs, fmt.Errorf("interval %s is below the %s minimum", c.Interval, minInterval))
	}
	if len(c.Areas) == 0 {
		errs = append(errs, errors.New("areas must list at least one state code, e.g. [OH]"))
	}
	if len(c.Subscribers) == 0 {
		errs = append(errs, errors.New("no subscribers configured"))
	}
	seen := map[string]bool{}
	for i, s := range c.Subscribers {
		switch {
		case s.Name == "":
			errs = append(errs, fmt.Errorf("subscribers[%d]: name is required", i))
		case seen[s.Name]:
			errs = append(errs, fmt.Errorf("subscribers[%d]: duplicate name %q", i, s.Name))
		}
		seen[s.Name] = true
		if s.Topic == "" {
			errs = append(errs, fmt.Errorf("subscriber %q: topic is required", s.Name))
		}
		if len(s.Zones) == 0 {
			errs = append(errs, fmt.Errorf("subscriber %q: zones is required (find yours with `weatheralerts lookup LAT,LON`)", s.Name))
		}
	}
	return errors.Join(errs...)
}
