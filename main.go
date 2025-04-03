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

	cloudflare "github.com/cloudflare/cloudflare-go"
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
	queryURL6 := flag.String("url6", "https://api6.ipify.org", "URL to query for the external IPv6 address")
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
			return pollAndUpdate(done, updater, ntfy, *queryURL, *queryURL6, *interval, *zone)
		}, func(error) { close(done) })
	}

	if err = actors.Run(); err != nil {
		log.Error("Error running actors", "error", err)
		os.Exit(1)
	}
}

type DNSUpdater struct {
	api   *cloudflare.API
	addr  netip.Addr
	addr6 netip.Addr
	ntfy  Notifier
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

func (d *DNSUpdater) UpdateIP4(ip netip.Addr, zone string) error {
	return d.updateIP(ip, zone, "A", &d.addr)
}

func (d *DNSUpdater) UpdateIP6(ip netip.Addr, zone string) error {
	return d.updateIP(ip, zone, "AAAA", &d.addr6)
}

func (d *DNSUpdater) updateIP(ip netip.Addr, zone string, recordType string, addrPtr *netip.Addr) error {
	d.Lock()
	defer d.Unlock()

	// Check if the address has actually changed
	if *addrPtr == ip {
		log.Debug("Address unchanged", "type", recordType, "ip", ip)
		return nil
	}

	err := updateRecord(zone, ip.String(), recordType)
	if err != nil {
		return fmt.Errorf("failed to update %s record: %w", recordType, err)
	}

	// Update the stored address
	*addrPtr = ip
	log.Info("New address stored", "type", recordType, "ip", ip)

	// Notify about the successful update
	d.ntfy.NotifySuccessUpdateIP(ip)
	return nil
}

type Notifier interface {
	NotifyFailedGetIP(error)
	NotifyFailedUpdateIP(error)
	NotifySuccessGetIP()
	NotifySuccessUpdateIP(netip.Addr)
}

func pollAndUpdate(done <-chan struct{}, updater *DNSUpdater, ntfy Notifier, url, url6 string, interval int, zone string) error {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			var wg sync.WaitGroup
			var addrV4, addrV6 netip.Addr
			var errV4, errV6 error

			wg.Add(2)

			// Fetch IPv4
			go func() {
				defer wg.Done()
				addrV4, errV4 = GetExternalIP(url)
				if errV4 != nil {
					log.Error("Failed to get external IPv4", "error", errV4)
					ntfy.NotifyFailedGetIP(fmt.Errorf("IPv4: %w", errV4))
				} else {
					log.Debug("Got external IPv4", "ip", addrV4)
					ntfy.NotifySuccessGetIP()
				}
			}()

			// Fetch IPv6
			go func() {
				defer wg.Done()
				addrV6, errV6 = GetExternalIP(url6)
				if errV6 != nil {
					log.Error("Failed to get external IPv6", "error", errV6)
					ntfy.NotifyFailedGetIP(fmt.Errorf("IPv6: %w", errV6))
				} else {
					log.Debug("Got external IPv6", "ip", addrV6)
					ntfy.NotifySuccessGetIP()
				}
			}()

			wg.Wait()

			// Update IPv4 if fetched successfully
			if errV4 == nil && addrV4.IsValid() {
				if err := updater.UpdateIP4(addrV4, zone); err != nil {
					log.Error("Failed to update IPv4", "error", err)
					ntfy.NotifyFailedUpdateIP(fmt.Errorf("IPv4: %w", err))
				}
			}

			// Update IPv6 if fetched successfully
			if errV6 == nil && addrV6.IsValid() {
				if err := updater.UpdateIP6(addrV6, zone); err != nil {
					log.Error("Failed to update IPv6", "error", err)
					ntfy.NotifyFailedUpdateIP(fmt.Errorf("IPv6: %w", err))
				}
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
