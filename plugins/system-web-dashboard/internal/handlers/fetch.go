package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	fetchTimeout    = 5 * time.Second
	maxResponseSize = 5 * 1024 * 1024 // 5 MB
	userAgent       = "Mozilla/5.0 (compatible; TeamAgenticaDashboard/1.0; +https://teamagentica.local)"
)

// FetchHandler implements GET /api/fetch?url=<encoded>.
//
// Server-side fetch used by the dashboard's Theme Manager to bypass browser
// CORS when importing tweakcn / shadcn theme JSON. Hardened against SSRF:
// only http(s), no loopback / private / link-local destinations, body capped.
func FetchHandler(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	if target == "" {
		writeError(w, http.StatusBadRequest, "missing url parameter")
		return
	}

	parsed, err := url.Parse(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid url: %v", err))
		return
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		writeError(w, http.StatusBadRequest, "only http and https schemes are allowed")
		return
	}

	host := parsed.Hostname()
	if host == "" {
		writeError(w, http.StatusBadRequest, "url has no host")
		return
	}

	if err := assertSafeHost(host); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("build request: %v", err))
		return
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	client := &http.Client{
		Timeout: fetchTimeout,
		// Don't blindly follow redirects to private addresses — re-check each hop.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return assertSafeHost(req.URL.Hostname())
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("fetch failed: %v", err))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("read body: %v", err))
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// assertSafeHost resolves the hostname and rejects any address that lands on a
// loopback, private, link-local, multicast, or unspecified IP — basic SSRF
// hardening.
func assertSafeHost(host string) error {
	if host == "" {
		return fmt.Errorf("empty host")
	}

	// If host is already an IP literal, check it directly.
	if ip := net.ParseIP(host); ip != nil {
		return checkIP(ip)
	}

	// Reject obvious private/internal hostnames before doing DNS.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("host %q is not allowed", host)
	}

	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %v", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("dns lookup returned no addresses")
	}
	for _, ip := range addrs {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

func checkIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("ip %s is in a disallowed range", ip.String())
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
