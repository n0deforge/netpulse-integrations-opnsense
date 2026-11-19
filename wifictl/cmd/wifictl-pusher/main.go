// wifictl-pusher pushes every AP of the self-hosted Wi-Fi controller to
// NetPulse as its own external unit.
//
// Environment:
//
//	NETPULSE_SERVER       http://netpulse:3000
//	NETPULSE_INTERVAL     30s (optional)
//	WIFICTL_URL           http://host:8088
//	WIFICTL_PASSWORD      controller ADMIN_PASSWORD (read-only use)
//	WIFICTL_UNITS         t18,asus  (controller AP ids to push)
//	WIFICTL_<ID>_SLUG     NetPulse slug for that AP (e.g. WIFICTL_T18_SLUG)
//	WIFICTL_<ID>_TOKEN    NetPulse agent token for that slug
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/gnacho/netpulse-integrations/internal/pusher"
	"github.com/gnacho/netpulse-integrations/wifictl/internal/wifictl"
)

var Version = "0.1.0"

func run(ctx context.Context) error {
	base, err := pusher.RequiredEnv("WIFICTL_URL")
	if err != nil {
		return err
	}
	units, err := pusher.RequiredEnv("WIFICTL_UNITS")
	if err != nil {
		return err
	}
	// ConfigFromEnv wants a token and slug; each unit overrides them below.
	for _, k := range []string{"NETPULSE_TOKEN", "NETPULSE_SLUG"} {
		if os.Getenv(k) == "" {
			os.Setenv(k, "unused")
		}
	}
	common, err := pusher.ConfigFromEnv("wifictl-pusher", Version)
	if err != nil {
		return err
	}
	client := wifictl.New(base, os.Getenv("WIFICTL_PASSWORD"), common.Interval/2)

	var wg sync.WaitGroup
	for _, id := range strings.Split(units, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := strings.ToUpper(id)
		slug, err := pusher.RequiredEnv("WIFICTL_" + key + "_SLUG")
		if err != nil {
			return err
		}
		token, err := pusher.RequiredEnv("WIFICTL_" + key + "_TOKEN")
		if err != nil {
			return err
		}
		cfg := common
		cfg.Name = fmt.Sprintf("wifictl-pusher[%s]", id)
		cfg.Slug, cfg.Token = slug, token
		collect := pusher.FromData(client.Collector(id))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pusher.Run(ctx, cfg, collect)
		}()
	}
	wg.Wait()
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("wifictl-pusher: stopped", "err", err)
		os.Exit(1)
	}
}
