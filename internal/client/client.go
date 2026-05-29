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
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

const defaultBaseURL = "https://api.ishosting.com"

// Client is the ISHosting API client.
type Client struct {
	BaseURL    string
	APIToken   string
	HTTPClient *http.Client
	orderMu    sync.Mutex // serializes orders since the API uses a shared cart
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
	}
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, query url.Values) ([]byte, error) {
	u := fmt.Sprintf("%s%s", c.BaseURL, path)
	if query != nil {
		u = fmt.Sprintf("%s?%s", u, query.Encode())
	}

	var reqBody io.Reader
	var jsonBody []byte
	if body != nil {
		var err error
		jsonBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBody)
	}

	log.Printf("[DEBUG] ISHosting API Request: %s %s", method, u)
	if jsonBody != nil {
		log.Printf("[DEBUG] Request Body: %s", string(jsonBody))
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
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	log.Printf("[DEBUG] ISHosting API Response: %s %s -> %d", method, u, resp.StatusCode)
	log.Printf("[DEBUG] Response Body: %s", string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
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
type VPSPatchRequest struct {
	Name *string  `json:"name,omitempty"`
	Tags []string `json:"tags,omitempty"`
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

// WaitForVPSActive polls until the VPS reaches an active state or times out.
func (c *Client) WaitForVPSActive(ctx context.Context, id string, timeout time.Duration) (*VPS, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		vps, err := c.GetVPS(ctx, id)
		if err == nil && (vps.Status.Code == "running" || vps.Status.State.Code == "Active") {
			return vps, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return nil, fmt.Errorf("timeout waiting for VPS %s to become active", id)
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
