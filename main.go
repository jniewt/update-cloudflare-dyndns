package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	log "log/slog"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/cloudflare/cloudflare-go"
	group "github.com/oklog/run"
)

func main() {

	bindAddr := flag.String("addr", ":8081", "address of the http server")
	polling := flag.Bool("polling", false, "use periodic polling in addition to webhook")
	interval := flag.Int("interval", 60, "interval in seconds for polling (only used if polling is enabled)")
	ntfyAddr := flag.String("ntfy", "", "ntfy.sh token to send notifications to when the address changes")
	zone := flag.String("zone", "", "Cloudflare zone to update (required when polling is enabled)")
	debug := flag.Bool("debug", false, "enable debug logging")
	queryURL := flag.String("url", "https://api.ipify.org", "URL to query for the external IP address")
	flag.Parse()

	if *debug {
		log.SetLogLoggerLevel(log.LevelDebug)
	}

	var ntfy Notifier
	if *ntfyAddr != "" {
		ntfy = &NtfyNotifier{token: *ntfyAddr, grace: 30 * time.Minute}
	} else {
		ntfy = &FakeNotifier{}
	}

	var actors group.Group
	// handle user signals, like Ctrl+C, to stop all actors
	actors.Add(group.SignalHandler(context.Background(), os.Interrupt, syscall.SIGTERM))

	updater, err := NewDNSUpdater(os.Getenv("CLOUDFLARE_API_TOKEN"), ntfy)
	if err != nil {
		log.Error("Failed to create DNS updater", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{Handler: NewServer(updater), Addr: *bindAddr}
	actors.Add(func() error {
		log.Info("Server started", "addr", srv.Addr)
		if err = srv.ListenAndServe(); err != nil {
			return fmt.Errorf("REST Server failed: %w", err)
		}
		return nil
	}, func(error) {
		_ = srv.Close()
	})

	// start polling if enabled
	if *polling {
		if *zone == "" {
			log.Error("Zone must be specified when polling is enabled")
			os.Exit(1)
		}
		done := make(chan struct{})
		actors.Add(func() error {
			return pollAndUpdate(done, updater, ntfy, *queryURL, *interval, *zone)
		}, func(error) { close(done) })
	}

	if err = actors.Run(); err != nil {
		log.Error("Error running actors", "error", err)
		os.Exit(1)
	}
}

type DNSUpdater struct {
	api  *cloudflare.API
	addr netip.Addr
	ntfy Notifier
	sync.Mutex
}

func NewDNSUpdater(token string, n Notifier) (*DNSUpdater, error) {
	api, err := cloudflare.NewWithAPIToken(token)
	if err != nil {
		return nil, err
	}
	return &DNSUpdater{
		api:  api,
		ntfy: n,
	}, nil
}

func (d *DNSUpdater) UpdateIP(ip netip.Addr, zone string) error {
	d.Lock()
	defer d.Unlock()
	if d.addr == ip {
		log.Debug("IP address unchanged", "ip", ip)
		return nil
	}
	err := updateRecord(zone, ip.String())
	if err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}
	d.addr = ip
	log.Info("New IP address", "ip", ip)
	d.ntfy.NotifySuccessUpdateIP(ip)
	return nil
}

type Notifier interface {
	NotifyFailedGetIP(error)
	NotifyFailedUpdateIP(error)
	NotifySuccessGetIP()
	NotifySuccessUpdateIP(netip.Addr)
}

func pollAndUpdate(done <-chan struct{}, updater *DNSUpdater, ntfy Notifier, url string, interval int, zone string) error {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			addr, err := GetExternalIP(url)
			if err != nil {
				log.Error("Failed to get external IP", "error", err)
				ntfy.NotifyFailedGetIP(err)
				continue
			}
			ntfy.NotifySuccessGetIP()
			if err = updater.UpdateIP(addr, zone); err != nil {
				log.Error("Failed to update IP", "error", err)
				ntfy.NotifyFailedUpdateIP(err)
				continue
			}
		}
	}
}

// GetExternalIP fetches the external IP address and returns it as a netip.Addr.
func GetExternalIP(url string) (netip.Addr, error) {
	resp, err := http.Get(url)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return netip.Addr{}, err
	}
	ipStr := string(ipBytes)
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP address format: %s", ipStr)
	}
	return ip, nil
}
