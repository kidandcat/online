package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/kidandcat/online/server"
)

func main() {
	tunnelManager := server.NewTunnelManager()
	staticManager := server.NewStaticFileManager()

	// WebSocket endpoint for tunnel connections
	http.HandleFunc("/ws/tunnel", func(w http.ResponseWriter, r *http.Request) {
		conn, err := server.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		tunnel, err := tunnelManager.CreateTunnel(conn)
		if err != nil {
			conn.WriteJSON(map[string]string{"error": err.Error()})
			return
		}

		// Send tunnel info to client
		proto := "https"
		if r.TLS == nil {
			proto = "http"
		}
		
		conn.WriteJSON(map[string]string{
			"id":  tunnel.ID,
			"url": fmt.Sprintf("%s://%s", proto, r.Host),
		})

		// Wait for tunnel to close (client disconnect)
		<-tunnel.Done()
		tunnelManager.RemoveTunnel()
	})

	// Static file upload endpoint
	http.HandleFunc("/upload", staticManager.HandleUpload)

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Main request handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Skip websocket and special endpoints
		if r.URL.Path == "/ws/tunnel" || r.URL.Path == "/upload" || r.URL.Path == "/health" {
			return
		}
		
		// Check if there's an active tunnel
		tunnel, exists := tunnelManager.GetActiveTunnel()
		if exists {
			// Forward all requests to the tunnel
			tunnel.ForwardRequest(w, r)
			return
		}

		// Handle static file serving (only when no tunnel is active)
		if strings.HasPrefix(r.URL.Path, "/") && len(r.URL.Path) > 1 {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			if len(parts) > 0 {
				storeID := parts[0]
				if store, exists := staticManager.GetStore(storeID); exists {
					store.ServeHTTP(w, r)
					return
				}
			}
		}

		// Default response
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>Online</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 800px; margin: 0 auto; padding: 20px; }
        pre { background: #f4f4f4; padding: 10px; border-radius: 5px; overflow-x: auto; }
        .section { margin: 20px 0; }
    </style>
</head>
<body>
    <h1>Online</h1>
    <p>Your own secure tunnel service running on Fly.io</p>
    
    <div class="section">
        <h2>Features</h2>
        <ul>
            <li>Expose local ports through secure tunnels from root domain</li>
            <li>Serve static files temporarily (when no tunnel is active)</li>
            <li>Automatic HTTPS with Fly.io</li>
            <li>24-hour tunnel/file expiration</li>
            <li>Single active tunnel at a time - takes over entire domain</li>
            <li>Full support for applications using absolute paths</li>
        </ul>
    </div>
    
    <div class="section">
        <h2>Quick Start</h2>
        <h3>1. Install the client</h3>
        <pre>go install github.com/kidandcat/online/cmd/online@latest</pre>
        
        <h3>2. Expose a local port</h3>
        <pre>online expose 3000</pre>
        <p>Note: The tunnel will take over the entire root domain. Only one tunnel can be active at a time.</p>
        
        <h3>3. Serve static files</h3>
        <pre>online serve ./my-folder</pre>
        <p>Note: Static files can only be served when no tunnel is active.</p>
    </div>
    
    <div class="section">
        <h2>API Endpoints</h2>
        <ul>
            <li><code>GET /health</code> - Health check</li>
            <li><code>WS /ws/tunnel</code> - WebSocket tunnel endpoint</li>
            <li><code>POST /upload</code> - Upload static files</li>
        </ul>
    </div>
</body>
</html>
		`)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

