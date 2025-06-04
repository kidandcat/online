package client

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	serverURL string
	conn      *websocket.Conn
}

type TunnelInfo struct {
	ID        string `json:"id"`
	Subdomain string `json:"subdomain"`
	URL       string `json:"url"`
	Error     string `json:"error,omitempty"`
}

type TunnelRequest struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type TunnelResponse struct {
	ID         string              `json:"id"`
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

func NewClient(serverURL string) *Client {
	return &Client{
		serverURL: serverURL,
	}
}

func (c *Client) ExposePort(port int, subdomain string) error {
	wsURL := c.getWebSocketURL(subdomain)

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	c.conn = conn

	// Read tunnel info
	var info TunnelInfo
	if err := conn.ReadJSON(&info); err != nil {
		return fmt.Errorf("failed to read tunnel info: %w", err)
	}

	if info.Error != "" {
		return fmt.Errorf("server error: %s", info.Error)
	}

	log.Printf("Tunnel created: %s", info.URL)
	log.Printf("Forwarding to localhost:%d", port)

	// Handle incoming requests
	for {
		var req TunnelRequest
		if err := conn.ReadJSON(&req); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				return fmt.Errorf("connection closed: %w", err)
			}
			return err
		}

		// Forward request to local port
		go c.handleRequest(req, port)
	}
}

func (c *Client) handleRequest(req TunnelRequest, port int) {
	// Create local request
	localURL := fmt.Sprintf("http://localhost:%d%s", port, req.Path)

	httpReq, err := http.NewRequest(req.Method, localURL, bytes.NewReader(req.Body))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		c.sendErrorResponse(req.ID, http.StatusInternalServerError, "Failed to create request")
		return
	}

	// Copy headers
	for k, v := range req.Headers {
		httpReq.Header[k] = v
	}

	// Make request to local server
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("Failed to forward request: %v", err)
		c.sendErrorResponse(req.ID, http.StatusBadGateway, "Failed to forward request")
		return
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v", err)
		c.sendErrorResponse(req.ID, http.StatusInternalServerError, "Failed to read response")
		return
	}

	// Send response back through WebSocket
	tunnelResp := TunnelResponse{
		ID:         req.ID,
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
	}

	if err := c.conn.WriteJSON(tunnelResp); err != nil {
		log.Printf("Failed to send response: %v", err)
	}
}

func (c *Client) sendErrorResponse(reqID string, statusCode int, message string) {
	resp := TunnelResponse{
		ID:         reqID,
		StatusCode: statusCode,
		Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		Body:       []byte(message),
	}

	if err := c.conn.WriteJSON(resp); err != nil {
		log.Printf("Failed to send error response: %v", err)
	}
}

func (c *Client) getWebSocketURL(subdomain string) string {
	u, _ := url.Parse(c.serverURL)

	// Convert HTTP(S) to WS(S)
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		u.Scheme = "wss"
	}

	u.Path = "/ws/tunnel"
	if subdomain != "" {
		q := u.Query()
		q.Set("subdomain", subdomain)
		u.RawQuery = q.Encode()
	}

	return u.String()
}

func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}
