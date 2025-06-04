# Online

Your own secure tunnel service running on Fly.io. Online allows you to:
- Expose local ports through secure HTTPS tunnels
- Temporarily serve static files and folders
- All with automatic SSL/TLS from Fly.io

## Features

- **Port Tunneling**: Expose any local port to the internet with a secure HTTPS URL
- **Static File Serving**: Upload and serve static files/folders temporarily (24-hour expiration)
- **Path-based Routing**: Uses paths instead of subdomains (no subdomain configuration needed)
- **WebSocket-based**: Efficient bidirectional communication
- **Automatic HTTPS**: All tunnels and static content served over HTTPS
- **Single Instance**: Designed for single Fly.io instance deployment

## Deployment to Fly.io

1. Install the Fly CLI:
```bash
curl -L https://fly.io/install.sh | sh
```

2. Login to Fly:
```bash
fly auth login
```

3. Create a new Fly app:
```bash
cd online
fly launch
```

4. Deploy:
```bash
fly deploy
```

5. Note your app URL (e.g., `https://your-app-name.fly.dev`)

## Client Installation

Install the CLI client:
```bash
go install github.com/kidandcat/online/cmd/online@latest
```

Or build from source:
```bash
cd online
go build -o online ./cmd/online
```

## Usage

### Expose a Local Port

Expose port 3000:
```bash
online expose 3000 --server https://your-app.fly.dev
```

This will create a tunnel accessible at a URL like:
```
https://your-app.fly.dev/tunnel/abc123/
```

### Serve Static Files

Serve a directory:
```bash
online serve ./dist --server https://your-app.fly.dev
```

Serve a single file:
```bash
online serve ./index.html --server https://your-app.fly.dev
```

### Configuration

Set the server URL as an environment variable to avoid typing it each time:
```bash
export ONLINE_SERVER="https://your-app.fly.dev"
online expose 3000
```

## How It Works

### Port Tunneling
1. Client connects to server via WebSocket
2. Server assigns a unique path (e.g., `/tunnel/abc123`)
3. HTTP requests to `https://your-app.fly.dev/tunnel/abc123/*` are forwarded through the WebSocket to your local port
4. Responses are sent back through the same connection

### Static File Serving
1. Client uploads files via multipart form
2. Server stores files in memory with a unique ID
3. Files are accessible at `https://your-app.fly.dev/{id}/filename`
4. Files expire after 24 hours

## Architecture

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│   Browser   │────────▶│  Fly.io App  │◀────────│   Client    │
└─────────────┘  HTTPS  └──────────────┘   WS    └─────────────┘
                               │                         │
                               │                         │
                               ▼                         ▼
                        ┌──────────────┐         ┌─────────────┐
                        │Static Storage│         │ Local Port  │
                        └──────────────┘         └─────────────┘
```

## Security Considerations

- All connections are encrypted with HTTPS/WSS
- Tunnels expire after 24 hours of inactivity
- Static files expire after 24 hours
- No authentication implemented (add as needed)
- Consider adding rate limiting for production use
- Path-based routing eliminates need for wildcard DNS/subdomain configuration

## Development

### Project Structure
```
online/
├── main.go                 # Server entry point
├── server/
│   ├── tunnel.go          # Tunnel management
│   └── static.go          # Static file serving
├── client/
│   ├── client.go          # Client library
│   └── static.go          # Static file upload
├── cmd/
│   └── online/
│       └── main.go        # CLI client
├── fly.toml               # Fly.io configuration
├── Dockerfile             # Container image
└── go.mod                 # Go dependencies
```

### Running Locally

Server:
```bash
go run main.go
```

Client (connecting to local server):
```bash
go run ./cmd/online expose 3000 --server http://localhost:8080
```

## License

MIT