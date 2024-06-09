package main

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// NtfyNotifier sends notifications to ntfy.sh. GetIP or UpdateIP has to fail for more than grace period to send a notification.
// If the failure continues, the notification will be sent again after the grace period.
type NtfyNotifier struct {
	token                       string
	grace                       time.Duration // grace period for notifications about failure
	lastGetIP, lastUpdateIP     time.Time     // last successful getIP and updateIP
	failedGetIP, failedUpdateIP time.Time     // last notification of failure
}

func (n *NtfyNotifier) NotifyFailedGetIP(err error) {
	if time.Since(n.lastGetIP) > n.grace && time.Since(n.failedGetIP) > n.grace {
		_ = n.Notify("warning", "Failed to get IP address: "+err.Error())
		n.failedGetIP = time.Now()
	}
}

func (n *NtfyNotifier) NotifyFailedUpdateIP(err error) {
	if time.Since(n.lastUpdateIP) > n.grace && time.Since(n.failedUpdateIP) > n.grace {
		_ = n.Notify("warning", "Failed to update IP address: "+err.Error())
		n.failedUpdateIP = time.Now()
	}
}

func (n *NtfyNotifier) NotifySuccessGetIP() {
	defer func() { n.lastGetIP = time.Now() }()
	if time.Since(n.lastGetIP) > n.grace {
		_ = n.Notify("globe_with_meridians", "Repaired: Get IP address")
		return
	}
}

func (n *NtfyNotifier) NotifySuccessUpdateIP(ip netip.Addr) {
	defer func() { n.lastUpdateIP = time.Now() }()
	if time.Since(n.lastUpdateIP) > time.Since(n.failedUpdateIP) {
		_ = n.Notify("globe_with_meridians", "Repaired: IP address updated to "+ip.String())
		return
	}
	_ = n.Notify("globe_with_meridians", "New IP address: "+ip.String())
}

func (n *NtfyNotifier) Notify(tags, msg string) error {
	url := fmt.Sprintf("https://ntfy.sh/%s", n.token)
	req, _ := http.NewRequest("POST", url,
		strings.NewReader(msg))
	req.Header.Set("Title", "DNS Updater")
	req.Header.Set("Tags", tags)

	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(r.Body)
		return fmt.Errorf("failed to send notification: %s - %s", r.Status, body)
	}

	return nil
}

type FakeNotifier struct {
}

func (f FakeNotifier) NotifyFailedGetIP(_ error) {

}

func (f FakeNotifier) NotifyFailedUpdateIP(_ error) {

}

func (f FakeNotifier) NotifySuccessGetIP() {

}

func (f FakeNotifier) NotifySuccessUpdateIP(_ netip.Addr) {

}
