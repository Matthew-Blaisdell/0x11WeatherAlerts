// Command weatheralerts polls NWS for active weather alerts and pushes the
// ones each subscriber cares about to their phone through ntfy.
//
//	weatheralerts -config config.yaml       run the service
//	weatheralerts lookup 41.04,-83.65       print the zone codes for a location
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed zone data: the container image has none

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/dispatch"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/metrics"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/notify"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/source/nws"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/store"
)

const userAgentApp = "0x11WeatherAlerts"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "lookup" {
		if err := lookup(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lookup:", err)
			os.Exit(1)
		}
		return
	}

	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer st.Close()

	httpClient := &http.Client{Timeout: 20 * time.Second}
	m := &metrics.Metrics{}
	d := &dispatch.Dispatcher{
		Source: &nws.Client{
			UserAgent: fmt.Sprintf("(%s, %s)", userAgentApp, cfg.Contact),
			Areas:     cfg.Areas,
			HTTP:      httpClient,
		},
		Store:       st,
		Notifier:    &notify.Ntfy{BaseURL: cfg.Ntfy.URL, Token: cfg.Ntfy.Token, HTTP: httpClient},
		Subscribers: cfg.Subscribers,
		Metrics:     m,
		Location:    cfg.location,
		OpsTopic:    cfg.OpsTopic,
		StaleAfter:  cfg.StaleAfter,
		Log:         log,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Healthy while polls keep succeeding; a few missed cycles are tolerated.
		last := m.LastSuccess()
		if last.IsZero() || time.Since(last) > 5*cfg.Interval {
			http.Error(w, "no successful poll since "+last.Format(time.RFC3339), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok, last poll", last.Format(time.RFC3339))
	})
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()

	log.Info("starting", "areas", cfg.Areas, "interval", cfg.Interval.String(),
		"subscribers", len(cfg.Subscribers), "listen", cfg.Listen)
	d.Run(ctx, cfg.Interval)

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func lookup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: weatheralerts lookup LAT,LON")
	}
	parts := strings.Split(args[0], ",")
	if len(parts) != 2 {
		return errors.New("expected LAT,LON, e.g. 41.04,-83.65")
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	c := &nws.Client{UserAgent: fmt.Sprintf("(%s, lookup)", userAgentApp), HTTP: &http.Client{Timeout: 20 * time.Second}}
	place, zone, county, err := c.PointZones(context.Background(), lat, lon)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n  areas: [%s]\n  zones: [%s, %s]\n", place, county[:min(2, len(county))], county, zone)
	return nil
}
