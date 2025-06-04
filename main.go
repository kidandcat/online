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
			"id":   tunnel.ID,
			"path": tunnel.Path,
			"url":  fmt.Sprintf("%s://%s/%s", proto, r.Host, tunnel.Path),
		})

		// Keep connection alive
		<-r.Context().Done()
		tunnelManager.RemoveTunnel(tunnel.Path)
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
		// Handle tunnel requests first
		if strings.HasPrefix(r.URL.Path, "/tunnel/") {
			tunnelPath := strings.TrimPrefix(r.URL.Path, "/")
			parts := strings.SplitN(tunnelPath, "/", 3)
			if len(parts) >= 2 {
				// Extract tunnel ID from path: /tunnel/abc123/...
				tunnelID := parts[0] + "/" + parts[1]
				tunnel, exists := tunnelManager.GetTunnel(tunnelID)
				if exists {
					// Update the request path to remove the tunnel prefix
					if len(parts) == 3 {
						r.URL.Path = "/" + parts[2]
					} else {
						r.URL.Path = "/"
					}
					tunnel.ForwardRequest(w, r)
					return
				}
			}
		}

		// Handle static file serving
		if strings.HasPrefix(r.URL.Path, "/") && len(r.URL.Path) > 1 {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			if len(parts) > 0 && !strings.HasPrefix(parts[0], "tunnel") {
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
            <li>Expose local ports through secure tunnels (path-based routing)</li>
            <li>Serve static files temporarily</li>
            <li>Automatic HTTPS with Fly.io</li>
            <li>24-hour tunnel/file expiration</li>
            <li>Single instance deployment - no subdomain configuration needed</li>
        </ul>
    </div>
    
    <div class="section">
        <h2>Quick Start</h2>
        <h3>1. Install the client</h3>
        <pre>go install github.com/kidandcat/online/cmd/online@latest</pre>
        
        <h3>2. Expose a local port</h3>
        <pre>online expose 3000</pre>
        
        <h3>3. Serve static files</h3>
        <pre>online serve ./my-folder</pre>
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

