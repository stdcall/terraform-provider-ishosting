package client

import (
	"bytes"
	"context"
	"crypto/rand"
	crypt "crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

const defaultBaseURL = "https://api.ishosting.com"

const (
	// defaultMinRequestInterval is the minimum spacing enforced between API
	// requests. The API rejects bursts with HTTP 429 ("Allowed no more than 1
	// request(s) in 10 seconds"), so all requests are throttled to at most one
	// per this interval. Override with the ISHOSTING_MIN_REQUEST_INTERVAL
	// environment variable (a Go duration such as "5s"; "0s" disables proactive
	// throttling and relies solely on 429 retries).
	defaultMinRequestInterval = 10 * time.Second

	// defaultMaxRetries bounds how many times a request is retried after a 429
	// response before giving up. Override with ISHOSTING_MAX_RETRIES.
	defaultMaxRetries = 5
)

// Client is the ISHosting API client.
type Client struct {
	BaseURL    string
	APIToken   string
	HTTPClient *http.Client
	orderMu    sync.Mutex // serializes orders since the API uses a shared cart

	// Rate limiting. The API permits only a small number of requests per window
	// (responding with HTTP 429 and a retry_after hint otherwise), so every
	// request is spaced at least minInterval apart across goroutines (via
	// nextAllowed) and retried up to maxRetries times on a 429.
	rateMu      sync.Mutex
	nextAllowed time.Time
	minInterval time.Duration
	maxRetries  int
}

// newUTLSTransport creates an HTTP/2 transport using uTLS with a Chrome TLS fingerprint
// to avoid Cloudflare bot detection that blocks Go's default TLS stack.
func newUTLSTransport() http.RoundTripper {
	dialTLS := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: 30 * time.Second}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		tlsConn := tls.UClient(conn, &tls.Config{ServerName: host}, tls.HelloChrome_Auto)
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, err
		}
		return tlsConn, nil
	}

	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *crypt.Config) (net.Conn, error) {
			return dialTLS(ctx, network, addr)
		},
	}
}

// NewClient creates a new ISHosting API client.
func NewClient(apiToken, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL:  baseURL,
		APIToken: apiToken,
		HTTPClient: &http.Client{
			Transport: newUTLSTransport(),
			Timeout:   120 * time.Second,
		},
		minInterval: envDuration("ISHOSTING_MIN_REQUEST_INTERVAL", defaultMinRequestInterval),
		maxRetries:  envInt("ISHOSTING_MAX_RETRIES", defaultMaxRetries),
	}
}

// envDuration reads a Go duration from the named environment variable, falling
// back to def when unset or invalid.
func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return d
	}
	log.Printf("[WARN] invalid %s=%q; using default %s", key, v, def)
	return def
}

// envInt reads a non-negative integer from the named environment variable,
// falling back to def when unset or invalid.
func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n
	}
	log.Printf("[WARN] invalid %s=%q; using default %d", key, v, def)
	return def
}

// reserveSlot blocks until this request's throttling slot is reached, spacing
// successive requests at least minInterval apart across all goroutines. It
// returns early if the context is cancelled.
func (c *Client) reserveSlot(ctx context.Context) error {
	c.rateMu.Lock()
	now := time.Now()
	slot := now
	if c.nextAllowed.After(now) {
		slot = c.nextAllowed
	}
	if c.minInterval > 0 {
		c.nextAllowed = slot.Add(c.minInterval)
	} else {
		c.nextAllowed = slot
	}
	c.rateMu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoffFor pushes the next allowed request time out by at least d, so that
// after a 429 every subsequent request (across goroutines) waits for the
// rate-limit window to clear rather than hammering the API in lockstep.
func (c *Client) backoffFor(d time.Duration) {
	if d <= 0 {
		return
	}
	c.rateMu.Lock()
	if t := time.Now().Add(d); t.After(c.nextAllowed) {
		c.nextAllowed = t
	}
	c.rateMu.Unlock()
}

// retryAfterFromResponse determines how long to wait before retrying a
// rate-limited (429) request, preferring the standard Retry-After header and
// falling back to the API's JSON body ({"data":{"retry_after":N}}). It returns
// 0 when no hint is available.
func retryAfterFromResponse(header http.Header, body []byte) time.Duration {
	if v := strings.TrimSpace(header.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	var parsed struct {
		Data struct {
			RetryAfter float64 `json:"retry_after"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Data.RetryAfter > 0 {
		return time.Duration(parsed.Data.RetryAfter * float64(time.Second))
	}
	return 0
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, query url.Values) ([]byte, error) {
	u := fmt.Sprintf("%s%s", c.BaseURL, path)
	if query != nil {
		u = fmt.Sprintf("%s?%s", u, query.Encode())
	}

	var jsonBody []byte
	if body != nil {
		var err error
		jsonBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
	}

	log.Printf("[DEBUG] ISHosting API Request: %s %s", method, u)
	if jsonBody != nil {
		log.Printf("[DEBUG] Request Body: %s", string(jsonBody))
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Throttle outgoing requests so we stay under the API's rate limit and
		// so concurrent requests (e.g. parallel resource creation) are spaced
		// out instead of colliding.
		if err := c.reserveSlot(ctx); err != nil {
			return nil, err
		}

		var reqBody io.Reader
		if jsonBody != nil {
			reqBody = bytes.NewReader(jsonBody)
		}

		req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("X-Api-Token", c.APIToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("executing request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading response body: %w", err)
		}

		log.Printf("[DEBUG] ISHosting API Response: %s %s -> %d", method, u, resp.StatusCode)
		log.Printf("[DEBUG] Response Body: %s", string(respBody))

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := retryAfterFromResponse(resp.Header, respBody)
			if wait <= 0 {
				wait = c.minInterval
			}
			if wait <= 0 {
				wait = defaultMinRequestInterval
			}
			// Back off globally so other in-flight requests also wait for the
			// window to clear before retrying.
			c.backoffFor(wait)
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
			if attempt < c.maxRetries {
				log.Printf("[WARN] ISHosting API rate limited (429) on %s %s; backing off ~%s then retrying (attempt %d/%d)",
					method, u, wait.Round(time.Second), attempt+1, c.maxRetries)
				continue
			}
			return nil, fmt.Errorf("rate limited after %d retries: %w", c.maxRetries, lastErr)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("request to %s %s failed after %d attempts", method, u, c.maxRetries+1)
}

// --- VPS Operations ---

// VPS represents a VPS instance matching the actual API response.
type VPS struct {
	ID       json.Number `json:"id"`
	Name     string      `json:"name"`
	Tags     []string    `json:"tags"`
	Location struct {
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"location"`
	Plan struct {
		Name      string `json:"name"`
		Code      string `json:"code"`
		Price     string `json:"price"`
		AutoRenew bool   `json:"auto_renew"`
		Period    struct {
			Name string `json:"name"`
			Code string `json:"code"`
		} `json:"period"`
	} `json:"plan"`
	Platform struct {
		Name   string `json:"name"`
		Code   string `json:"code"`
		Config struct {
			CPU struct {
				Value string `json:"value"`
				Name  string `json:"name"`
				Code  string `json:"code"`
			} `json:"cpu"`
			RAM struct {
				Value string `json:"value"`
				Name  string `json:"name"`
				Code  string `json:"code"`
			} `json:"ram"`
			Drive struct {
				Value string `json:"value"`
				Name  string `json:"name"`
				Code  string `json:"code"`
			} `json:"drive"`
			OS struct {
				Value string `json:"value"`
				Name  string `json:"name"`
				Code  string `json:"code"`
			} `json:"os"`
		} `json:"config"`
	} `json:"platform"`
	Network struct {
		PublicIP  string `json:"public_ip"`
		Protocols struct {
			IPv4 []IPAddress `json:"ipv4"`
			IPv6 []IPAddress `json:"ipv6"`
		} `json:"protocols"`
		Port      string `json:"port"`
		Bandwidth string `json:"bandwidth"`
	} `json:"network"`
	Access struct {
		SSH *struct {
			Port  int       `json:"port"`
			Users []SSHUser `json:"users"`
			Keys  []struct {
				ID string `json:"id"`
			} `json:"keys"`
		} `json:"ssh"`
	} `json:"access"`
	Status struct {
		Name  string `json:"name"`
		Code  string `json:"code"`
		State struct {
			Name string `json:"name"`
			Code string `json:"code"`
		} `json:"state"`
	} `json:"status"`
	CreatedAt json.Number `json:"created_at"`
	UpdatedAt json.Number `json:"updated_at"`
}

type IPAddress struct {
	Address string `json:"address"`
	Mask    string `json:"mask"`
	Gateway string `json:"gateway"`
	RDNS    string `json:"rdns"`
	IsMain  bool   `json:"is_main"`
}

type SSHUser struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	IsEnabled bool   `json:"is_enabled"`
}

// GetVPS retrieves a VPS by ID.
func (c *Client) GetVPS(ctx context.Context, id string) (*VPS, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/vps/%s", id), nil, nil)
	if err != nil {
		return nil, err
	}

	var vps VPS
	if err := json.Unmarshal(respBody, &vps); err != nil {
		return nil, fmt.Errorf("unmarshaling VPS response: %w", err)
	}

	return &vps, nil
}

// VPSPatchRequest represents the PATCH request body for updating a VPS.
//
// The API treats name and tags as required on every PATCH (omitting name
// returns 400 "should have required property 'name'"), so Tags has no
// omitempty: a cleared list must be sent as an explicit empty array rather than
// dropped. Callers always set Tags to a non-nil slice.
type VPSPatchRequest struct {
	Name *string  `json:"name,omitempty"`
	Tags []string `json:"tags"`
	Plan *struct {
		AutoRenew *bool `json:"auto_renew,omitempty"`
	} `json:"plan,omitempty"`
}

// UpdateVPS updates a VPS instance.
func (c *Client) UpdateVPS(ctx context.Context, id string, req VPSPatchRequest) (*VPS, error) {
	respBody, err := c.doRequest(ctx, http.MethodPatch, fmt.Sprintf("/vps/%s", id), req, nil)
	if err != nil {
		return nil, err
	}

	var vps VPS
	if err := json.Unmarshal(respBody, &vps); err != nil {
		return nil, fmt.Errorf("unmarshaling VPS update response: %w", err)
	}

	return &vps, nil
}

// --- Order Operations ---

// OrderItem represents an item in an order.
//
// The plan code fully determines the location, billing period and base
// hardware (the API derives location from the plan code, so there is no
// separate location field). Use Additions to customise OS, RAM, drive,
// extra IPs, control panel, etc.
type OrderItem struct {
	Action    string          `json:"action"`
	Identity  string          `json:"identity,omitempty"`
	Type      string          `json:"type"`
	Plan      string          `json:"plan"`
	Quantity  int             `json:"quantity"`
	Options   *OrderOptions   `json:"options,omitempty"`
	Additions []OrderAddition `json:"additions,omitempty"`
	Comment   string          `json:"comment,omitempty"`
}

type OrderOptions struct {
	VNC *OrderVNC `json:"vnc,omitempty"`
	SSH *OrderSSH `json:"ssh,omitempty"`
}

type OrderVNC struct {
	IsEnabled bool `json:"is_enabled"`
}

type OrderSSH struct {
	IsEnabled bool     `json:"is_enabled"`
	Keys      []string `json:"keys,omitempty"`
}

// OrderAddition represents a plan add-on. Most add-ons are selected by Code
// (e.g. {"code":"2g","category":"ram"} or {"code":"linux/ubuntu24#64","category":"os"}),
// while quantity-based add-ons such as extra IPs use Quantity
// (e.g. {"quantity":2,"category":"ip"}). Valid category/code values come from
// the /vps/configs/{plan} endpoint.
type OrderAddition struct {
	Code     string `json:"code,omitempty"`
	Category string `json:"category"`
	Quantity *int   `json:"quantity,omitempty"`
}

// NewOrderIdentity returns a random 16-character hex identity used to correlate
// an order item within a multi-item order, matching the official API client.
func NewOrderIdentity() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type OrderRequest struct {
	Items  []OrderItem `json:"items"`
	Promos []string    `json:"promos,omitempty"`
}

type InvoiceResponse struct {
	ID     json.Number `json:"id"`
	Cipher string      `json:"cipher"`
	Status struct {
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"status"`
	Services []struct {
		Action  string `json:"action"`
		Type    string `json:"type"`
		Service struct {
			ID json.Number `json:"id"`
		} `json:"service"`
	} `json:"services"`
}

// LockOrder acquires the order mutex. Since the ISHosting API uses a shared cart,
// only one order can be in-flight at a time. Call UnlockOrder when the order is
// fully processed (i.e. the VPS is active or the order has failed).
func (c *Client) LockOrder() {
	c.orderMu.Lock()
}

// UnlockOrder releases the order mutex.
func (c *Client) UnlockOrder() {
	c.orderMu.Unlock()
}

// CreateOrder creates a new order (provisions a VPS).
// Caller must hold the order lock (via LockOrder/UnlockOrder).
func (c *Client) CreateOrder(ctx context.Context, req OrderRequest) (*InvoiceResponse, error) {
	respBody, err := c.doRequest(ctx, http.MethodPost, "/billing/order", req, nil)
	if err != nil {
		return nil, err
	}

	var resp InvoiceResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshaling order response: %w", err)
	}

	return &resp, nil
}

// PayInvoiceRequest represents the request body for paying an invoice.
type PayInvoiceRequest struct {
	Balance bool `json:"balance"`
	Renew   bool `json:"renew"`
}

// PayInvoice pays an invoice by its ID.
func (c *Client) PayInvoice(ctx context.Context, invoiceID string, req PayInvoiceRequest) ([]byte, error) {
	return c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/billing/invoice/%s/pay", invoiceID), req, nil)
}

// CancelInvoice cancels an unpaid invoice by its ID.
func (c *Client) CancelInvoice(ctx context.Context, invoiceID string) error {
	_, err := c.doRequest(ctx, http.MethodPatch, fmt.Sprintf("/billing/invoice/%s/cancel", invoiceID), nil, nil)
	return err
}

// vpsPollInterval is how often WaitForVPSActive re-checks the VPS while it
// provisions.
const vpsPollInterval = 10 * time.Second

// vpsIsReady reports whether a provisioning VPS has finished and is usable.
// The live API returns lowercase status codes ("activating" -> "running") and
// capitalized state codes ("Ordered" -> "Active"), so matching is
// case-insensitive to tolerate either convention.
func vpsIsReady(vps *VPS) bool {
	return strings.EqualFold(vps.Status.Code, "running") ||
		strings.EqualFold(vps.Status.State.Code, "active")
}

// vpsFailureReason returns a non-empty reason when the VPS has entered a
// terminal state from which it will never become active, so polling can stop
// early instead of waiting for the full timeout.
func vpsFailureReason(vps *VPS) string {
	codes := []string{vps.Status.Code, vps.Status.State.Code}
	for _, code := range codes {
		switch {
		case strings.EqualFold(code, "rejected"):
			return "rejected"
		case strings.EqualFold(code, "cancelled"), strings.EqualFold(code, "canceled"):
			return "cancelled"
		case strings.EqualFold(code, "terminated"):
			return "terminated"
		}
	}
	return ""
}

// WaitForVPSActive polls GET /vps/{id} until the VPS becomes active/running,
// enters a terminal failure state, or the timeout elapses. The most recently
// fetched VPS is always returned (including on timeout) so callers can persist
// whatever fields are already known. Transient fetch errors (e.g. a brief 404
// right after ordering) are logged and retried rather than aborting the wait.
func (c *Client) WaitForVPSActive(ctx context.Context, id string, timeout time.Duration) (*VPS, error) {
	deadline := time.Now().Add(timeout)
	var last *VPS
	for {
		vps, err := c.GetVPS(ctx, id)
		if err != nil {
			log.Printf("[DEBUG] polling VPS %s (will retry): %v", id, err)
		} else {
			last = vps
			if vpsIsReady(vps) {
				return vps, nil
			}
			if reason := vpsFailureReason(vps); reason != "" {
				return vps, fmt.Errorf("VPS %s entered terminal state %q (status=%q, state=%q)",
					id, reason, vps.Status.Code, vps.Status.State.Code)
			}
		}

		if !time.Now().Before(deadline) {
			statusCode, stateCode := "unknown", "unknown"
			if last != nil {
				statusCode = last.Status.Code
				stateCode = last.Status.State.Code
			}
			return last, fmt.Errorf("timeout waiting for VPS %s to become active after %s (last status=%q, state=%q)",
				id, timeout, statusCode, stateCode)
		}

		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(vpsPollInterval):
		}
	}
}

// --- SSH Key Operations ---

// SSHKey represents an SSH key. The API returns ID as a string in list responses
// and as an int in create responses, so we use json.Number to handle both.
type SSHKey struct {
	ID          json.Number `json:"id"`
	Fingerprint string      `json:"fingerprint"`
	Title       string      `json:"title"`
	Public      string      `json:"public"`
}

type SSHKeyCreateRequest struct {
	Title  string `json:"title"`
	Public string `json:"public"`
}

// ListSSHKeys lists all SSH keys.
func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/settings/ssh", nil, nil)
	if err != nil {
		return nil, err
	}

	var keys []SSHKey
	if err := json.Unmarshal(respBody, &keys); err != nil {
		return nil, fmt.Errorf("unmarshaling SSH keys response: %w", err)
	}

	return keys, nil
}

// CreateSSHKey creates a new SSH key.
func (c *Client) CreateSSHKey(ctx context.Context, req SSHKeyCreateRequest) (*SSHKey, error) {
	respBody, err := c.doRequest(ctx, http.MethodPost, "/settings/ssh", req, nil)
	if err != nil {
		return nil, err
	}

	var key SSHKey
	if err := json.Unmarshal(respBody, &key); err != nil {
		return nil, fmt.Errorf("unmarshaling SSH key response: %w", err)
	}

	return &key, nil
}

// GetSSHKey retrieves a single SSH key by ID (fetches all and filters).
func (c *Client) GetSSHKey(ctx context.Context, id string) (*SSHKey, error) {
	keys, err := c.ListSSHKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key.ID.String() == id {
			return &key, nil
		}
	}
	return nil, fmt.Errorf("SSH key %s not found", id)
}

// DeleteSSHKey deletes an SSH key by ID.
func (c *Client) DeleteSSHKey(ctx context.Context, id string) error {
	_, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/settings/ssh/%s", id), nil, nil)
	return err
}

// --- VPS IP Operations ---

// IPPatchRequest represents the request to update an IP address.
type IPPatchRequest struct {
	IsMain *bool   `json:"is_main,omitempty"`
	RDNS   *string `json:"rdns,omitempty"`
}

// IPResponse represents the API response for IP operations.
type IPResponse struct {
	Data IPAddress `json:"data"`
}

// UpdateVPSIP updates an IP address configuration on a VPS.
func (c *Client) UpdateVPSIP(ctx context.Context, vpsID, protocol, ip string, req IPPatchRequest) (*IPAddress, error) {
	respBody, err := c.doRequest(ctx, http.MethodPatch, fmt.Sprintf("/vps/%s/network/%s/%s", vpsID, protocol, ip), req, nil)
	if err != nil {
		return nil, err
	}

	var resp IPResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshaling IP response: %w", err)
	}

	return &resp.Data, nil
}

// DeleteVPSIP removes an IP address from a VPS.
func (c *Client) DeleteVPSIP(ctx context.Context, vpsID, protocol, ip string) error {
	_, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/vps/%s/network/%s/%s", vpsID, protocol, ip), nil, nil)
	return err
}

// GetVPSIP retrieves a specific IP from a VPS by reading the full VPS and filtering.
func (c *Client) GetVPSIP(ctx context.Context, vpsID, protocol, ip string) (*IPAddress, error) {
	vps, err := c.GetVPS(ctx, vpsID)
	if err != nil {
		return nil, err
	}

	var ips []IPAddress
	switch protocol {
	case "ipv4":
		ips = vps.Network.Protocols.IPv4
	case "ipv6":
		ips = vps.Network.Protocols.IPv6
	default:
		return nil, fmt.Errorf("unknown protocol: %s", protocol)
	}

	for _, addr := range ips {
		if addr.Address == ip {
			return &addr, nil
		}
	}

	return nil, fmt.Errorf("IP %s not found on VPS %s (protocol %s)", ip, vpsID, protocol)
}

// GetVPSIPs returns all IPs for a VPS grouped by protocol.
func (c *Client) GetVPSIPs(ctx context.Context, vpsID string) ([]IPAddress, []IPAddress, string, error) {
	vps, err := c.GetVPS(ctx, vpsID)
	if err != nil {
		return nil, nil, "", err
	}
	return vps.Network.Protocols.IPv4, vps.Network.Protocols.IPv6, vps.Network.PublicIP, nil
}

// --- Plans & Configs ---

// VPSPlanConfigItem is a hardware/OS component of a plan. The API returns these
// as value/name/code triples (e.g. RAM {value:"1GB", name:"1 Gb", code:"1g"}).
type VPSPlanConfigItem struct {
	Value string `json:"value"`
	Name  string `json:"name"`
	Code  string `json:"code"`
}

// VPSPlanPeriod is one of the available billing periods for a plan, each mapping
// to its own plan code (e.g. {code:"1y", plan:"791_1y", price:"71.3$"}).
type VPSPlanPeriod struct {
	Name  string `json:"name"`
	Code  string `json:"code"`
	Plan  string `json:"plan"`
	Price string `json:"price"`
}

// VPSPlan represents a single entry from /vps/plans. Note the API returns a bare
// JSON array of these objects (no "data" wrapper), prices are strings like
// "6.99$", and the period is an object rather than a bare string.
type VPSPlan struct {
	Plan struct {
		Name        string `json:"name"`
		Code        string `json:"code"`
		Description string `json:"description"`
		Price       string `json:"price"`
		Category    struct {
			Name string `json:"name"`
			Code string `json:"code"`
		} `json:"category"`
		Period struct {
			Name string `json:"name"`
			Code string `json:"code"`
		} `json:"period"`
	} `json:"plan"`
	Location struct {
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"location"`
	Platform struct {
		Name   string `json:"name"`
		Code   string `json:"code"`
		Config struct {
			CPU   VPSPlanConfigItem `json:"cpu"`
			RAM   VPSPlanConfigItem `json:"ram"`
			Drive VPSPlanConfigItem `json:"drive"`
			OS    VPSPlanConfigItem `json:"os"`
		} `json:"config"`
	} `json:"platform"`
	Periods []VPSPlanPeriod `json:"periods"`
}

// ListVPSPlans lists available VPS plans, optionally filtered by location and/or
// platform codes. Filters use repeated "locations"/"platforms" query parameters
// (the older "locations[]" bracket form is silently ignored by the API).
func (c *Client) ListVPSPlans(ctx context.Context, locations, platforms []string) ([]VPSPlan, error) {
	query := url.Values{}
	for _, l := range locations {
		query.Add("locations", l)
	}
	for _, p := range platforms {
		query.Add("platforms", p)
	}
	if len(query) == 0 {
		query = nil
	}

	respBody, err := c.doRequest(ctx, http.MethodGet, "/vps/plans", nil, query)
	if err != nil {
		return nil, err
	}

	var plans []VPSPlan
	if err := json.Unmarshal(respBody, &plans); err != nil {
		return nil, fmt.Errorf("unmarshaling VPS plans response: %w", err)
	}

	return plans, nil
}

// VPSConfigLocation maps a country to the plan code that provisions a plan in
// that country, as returned in the "locations" array of /vps/configs/{plan}.
type VPSConfigLocation struct {
	Name  string `json:"name"`
	Code  string `json:"code"`
	Plan  string `json:"plan"`
	Price string `json:"price"`
}

// GetVPSConfigs retrieves available configuration options for a plan code. The
// API returns a top-level object (periods, locations, platforms, network,
// security, tools), which is returned here verbatim as raw JSON.
func (c *Client) GetVPSConfigs(ctx context.Context, planCode string) (json.RawMessage, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/vps/configs/%s", planCode), nil, nil)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(respBody), nil
}

// ParseConfigLocations extracts the per-country plan codes from a raw configs
// response (the "locations" array).
func ParseConfigLocations(raw json.RawMessage) ([]VPSConfigLocation, error) {
	var parsed struct {
		Locations []VPSConfigLocation `json:"locations"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parsing VPS configs locations: %w", err)
	}
	return parsed.Locations, nil
}
