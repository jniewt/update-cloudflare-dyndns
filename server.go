package main

import (
	"context"
	"fmt"
	log "log/slog"
	"net/http"
	"net/netip"
	"os"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

type Server struct {
	updater *DNSUpdater
	router  *http.ServeMux
}

func NewServer(updater *DNSUpdater) *Server {
	s := &Server{
		updater: updater,
		router:  http.NewServeMux(),
	}
	s.routes()
	return s
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
	var addr netip.Addr
	var err error
	if addr, err = netip.ParseAddr(ipv4Query[0]); !addr.Is4() || err != nil {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid ipv4 address %s", ipv4Query[0]))
		return
	}

	err = s.updater.UpdateIP4(addr, zone[0])
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

// updateRecord updates a DNS record with the given IP address and record type. Use "A" for IPv4 and "AAAA" for IPv6.
func updateRecord(zoneName string, ip string, recordType string) error {
	api, err := cloudflare.NewWithAPIToken(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if err != nil {
		return err
	}

	zoneID, err := api.ZoneIDByName(zoneName)
	if err != nil {
		return fmt.Errorf("failed to find zone %s: %w", zoneName, err)
	}
	id, err := findDNSRecordID(api, zoneID, recordType)
	if err != nil {
		return fmt.Errorf("failed to find %s record for zone %s: %w", recordType, zoneName, err)
	}
	updateParams := cloudflare.UpdateDNSRecordParams{
		Content: ip,
		Type:    recordType,
		ID:      id,
	}
	rec, err := api.UpdateDNSRecord(context.Background(), cloudflare.ZoneIdentifier(zoneID), updateParams)
	if err != nil {
		return fmt.Errorf("failed to update %s record for zone %s: %w", recordType, zoneName, err)
	}
	log.Info("Record updated successfully.", "recordType", rec.Type, "content", rec.Content, "zone", zoneName)

	return nil
}

// findDNSRecordID finds the ID of the first DNS record matching the specified type.
func findDNSRecordID(api *cloudflare.API, zoneID string, recordType string) (string, error) {
	recs, _, err := api.ListDNSRecords(context.Background(), cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{Type: recordType})
	if err != nil {
		return "", err
	}

	// Since we filter by Type in the API call, the first record found should be the correct one.
	// Cloudflare typically only allows one A or AAAA record for the root zone name unless using load balancing etc.
	if len(recs) > 0 {
		log.Debug("Found DNS record", "type", recordType, "id", recs[0].ID, "content", recs[0].Content)
		return recs[0].ID, nil
	}

	return "", fmt.Errorf("no %s record found", recordType)
}
