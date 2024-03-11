package main

import (
	"context"
	"errors"
	"fmt"
	log "log/slog"
	"net/http"
	"net/netip"
	"os"

	"github.com/cloudflare/cloudflare-go"
)

type Server struct {
	api    *cloudflare.API
	router *http.ServeMux
}

func NewServer(token string) (*Server, error) {
	api, err := cloudflare.NewWithAPIToken(token)
	if err != nil {
		return nil, err
	}
	s := &Server{
		api:    api,
		router: http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.router.HandleFunc("/", s.HandleIndex)
}

func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	// log details of the request
	log.Info("New request.", "method", r.Method, "url", r.URL, "agent", r.Header["User-Agent"])
	query := r.URL.Query()

	zone, ok := query["zone"]
	if !ok {
		httpError(w, http.StatusBadRequest, "missing zone parameter in query")
		return
	}

	ipv4Query, ok := query["ip"]
	if !ok {
		httpError(w, http.StatusBadRequest, "missing ip parameter in query")
		return
	}
	if addr, err := netip.ParseAddr(ipv4Query[0]); !addr.Is4() || err != nil {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid ipv4 address %s", ipv4Query[0]))
		return
	}

	err := updateRecord(zone[0], ipv4Query[0])
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// otherwise send a success response
	httpSuccess(w, http.StatusOK, "record updated")
}

// httpError is a helper function to send an error response with a given status code and message as json
func httpError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := w.Write([]byte(`{"error": "` + message + `"}`))
	if err != nil {
		log.Warn("Failed to write response.", "error", err)
	}
}

// httpSuccess is a helper function to send a success response with a given status code and message as json
func httpSuccess(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := w.Write([]byte(`{"message": "` + message + `"}`))
	if err != nil {
		log.Warn("Failed to write response.", "error", err)
	}
}

func updateRecord(zoneName string, ipv4 string) error {
	api, err := cloudflare.NewWithAPIToken(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if err != nil {
		return err
	}

	zoneID, err := api.ZoneIDByName(zoneName)
	if err != nil {
		return fmt.Errorf("failed to find zone %s: %w", zoneName, err)
	}
	id, err := findIP4ARecordID(api, zoneID)
	if err != nil {
		return fmt.Errorf("failed to find record for zone %s: %w", zoneName, err)
	}
	update := cloudflare.UpdateDNSRecordParams{
		Content: ipv4,
		ID:      id,
	}
	rec, err := api.UpdateDNSRecord(context.Background(), cloudflare.ZoneIdentifier(zoneID), update)
	if err != nil {
		return fmt.Errorf("failed to update record for zone %s: %w", zoneName, err)
	}
	log.Info("Record updated successfully.", "record", rec)

	return nil
}

func findIP4ARecordID(api *cloudflare.API, zoneID string) (string, error) {
	recs, _, err := api.ListDNSRecords(context.Background(), cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{})
	if err != nil {
		return "", err
	}
	var id string
	for _, r := range recs {
		if r.Type == "A" {
			addr := netip.MustParseAddr(r.Content)
			if addr.Is4() {
				id = r.ID
				break
			}
		}
	}
	if id == "" {
		return "", errors.New("no IPv4 A record found")
	}
	return id, nil
}
