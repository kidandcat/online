package client

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	serverURL string
	conn      *websocket.Conn
	mu        sync.Mutex
}

type TunnelInfo struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Error string `json:"error,omitempty"`
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

func (c *Client) ExposePort(port int) error {
	wsURL := c.getWebSocketURL()

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

	// Copy headers, but skip SSL-related ones to avoid confusing local servers
	skipHeaders := map[string]bool{
		"x-forwarded-proto": true,
		"x-forwarded-ssl":   true,
		"x-forwarded-port":  true,
		"x-forwarded-for":   true,
	}
	
	for k, v := range req.Headers {
		if !skipHeaders[strings.ToLower(k)] {
			httpReq.Header[k] = v
		}
	}
	
	// Set explicit HTTP protocol header for local connection
	httpReq.Header.Set("X-Forwarded-Proto", "http")

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

	c.mu.Lock()
	err = c.conn.WriteJSON(tunnelResp)
	c.mu.Unlock()
	if err != nil {
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

	c.mu.Lock()
	err := c.conn.WriteJSON(resp)
	c.mu.Unlock()
	if err != nil {
		log.Printf("Failed to send error response: %v", err)
	}
}

func (c *Client) getWebSocketURL() string {
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
	return u.String()
}

func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}
