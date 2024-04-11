package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type NtfyNotifier struct {
	token string
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

func (n *FakeNotifier) Notify(_, _ string) error {
	return nil
}
