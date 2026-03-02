// Package terminal implements the coral terminal command (RFD 094).
package terminal

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed web/dashboard.html
var dashboardHTML []byte

// RenderSpec describes how a skill result should be visualised in the browser.
// Forwarded verbatim from SkillResult.render in the TypeScript SDK.
type RenderSpec struct {
	// Type selects the renderer: "table", "bar", "timeseries", or any custom string.
	Type string `json:"type"`
	// Title is shown in the dashboard panel header.
	Title string `json:"title,omitempty"`
	// Payload is renderer-specific data whose shape is documented per Type.
	Payload any `json:"payload"`
}

// RenderEvent is sent from the Go executor to connected browser clients
// whenever a skill result includes a render spec.
type RenderEvent struct {
	// ID is a stable UUID per skill run.
	ID string `json:"id"`
	// Ts is the event timestamp in Unix milliseconds.
	Ts int64 `json:"ts"`
	// SkillName is populated from the executor context when available.
	SkillName string `json:"skillName,omitempty"`
	// Spec is forwarded verbatim from SkillResult.render.
	Spec RenderSpec `json:"spec"`
}

// Server is the embedded HTTP / WebSocket server started by coral terminal.
// It serves the browser dashboard and broadcasts RenderEvents to all clients.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	port       int

	mu      sync.Mutex
	clients map[*websocket.Conn]chan []byte
}

// global registry — nil when coral terminal is not running.
var (
	activeMu     sync.RWMutex
	activeServer *Server
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Validate the Origin header to prevent cross-site WebSocket hijacking.
		// Only connections from localhost are accepted (security per RFD 094).
		origin := r.Header.Get("Origin")
		if origin == "" {
			// No origin header (e.g. curl); allow for CLI tooling convenience.
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		host, _, err := net.SplitHostPort(u.Host)
		if err != nil {
			host = u.Host
		}
		return host == "localhost" || host == "127.0.0.1" || host == "[::1]"
	},
}

// StartServer binds to an ephemeral localhost port and starts the HTTP server.
// It registers itself as the active server so that coral run can push events.
// Call Stop to shut down.
func StartServer() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("terminal server listen: %w", err)
	}

	s := &Server{
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
		clients:  make(map[*websocket.Conn]chan []byte),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/ws", s.handleWebSocket)

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = s.httpServer.Serve(ln)
	}()

	activeMu.Lock()
	activeServer = s
	activeMu.Unlock()

	return s, nil
}

// GetActiveServer returns the running server, or nil when coral terminal is not running.
// Used by coral run to push render events without a hard dependency on the terminal command.
func GetActiveServer() *Server {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return activeServer
}

// Port returns the ephemeral port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

// Push broadcasts a RenderEvent to all connected WebSocket clients.
// It is safe to call from any goroutine and is a no-op when no clients are connected.
func (s *Server) Push(event RenderEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for conn, ch := range s.clients {
		select {
		case ch <- data:
		default:
			// Client is too slow; close and remove it.
			conn.Close()
			delete(s.clients, conn)
		}
	}
}

// Stop shuts down the HTTP server and clears the global registry.
func (s *Server) Stop() {
	activeMu.Lock()
	if activeServer == s {
		activeServer = nil
	}
	activeMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.httpServer.Shutdown(ctx)
}

// handleDashboard serves the embedded browser dashboard HTML.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(dashboardHTML)
}

// handleWebSocket upgrades the connection and enters the read/write loops.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	ch := make(chan []byte, 64)

	s.mu.Lock()
	s.clients[conn] = ch
	s.mu.Unlock()

	// Write loop: send buffered messages to this client.
	go func() {
		defer func() {
			conn.Close()
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
		}()

		for data := range ch {
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}()

	// Read loop: consume and discard client→server messages (reserved for future use).
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	// Signal write loop to exit.
	close(ch)
}
