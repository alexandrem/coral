package terminal_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/cli/terminal"
)

func wsAddr(port int) string   { return fmt.Sprintf("ws://localhost:%d/ws", port) }
func httpAddr(port int) string { return fmt.Sprintf("http://localhost:%d/", port) }

func dialWS(t *testing.T, port int) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(
		wsAddr(port),
		http.Header{"Origin": {"http://localhost"}},
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestStartServer_GetActiveServer(t *testing.T) {
	// Clear any residual server from a previous test.
	if prev := terminal.GetActiveServer(); prev != nil {
		prev.Stop()
	}

	require.Nil(t, terminal.GetActiveServer(), "expected nil before StartServer")

	srv, err := terminal.StartServer()
	require.NoError(t, err)

	assert.NotNil(t, terminal.GetActiveServer(), "expected active server after StartServer")
	assert.Greater(t, srv.Port(), 0, "expected ephemeral port > 0")

	srv.Stop()
	assert.Nil(t, terminal.GetActiveServer(), "expected nil after Stop")
}

func TestServer_Push_BroadcastsToClients(t *testing.T) {
	srv, err := terminal.StartServer()
	require.NoError(t, err)
	defer srv.Stop()

	conn1 := dialWS(t, srv.Port())
	conn2 := dialWS(t, srv.Port())

	// Give the server a moment to register both clients.
	time.Sleep(30 * time.Millisecond)

	event := terminal.RenderEvent{
		ID:        "test-id",
		Ts:        time.Now().UnixMilli(),
		SkillName: "test-skill",
		Spec:      terminal.RenderSpec{Type: "table", Title: "Test Table"},
	}
	srv.Push(event)

	// Both clients should receive the event.
	for i, conn := range []*websocket.Conn{conn1, conn2} {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		_, msg, err := conn.ReadMessage()
		require.NoError(t, err, "client %d did not receive message", i+1)

		var got terminal.RenderEvent
		require.NoError(t, json.Unmarshal(msg, &got))
		assert.Equal(t, event.ID, got.ID)
		assert.Equal(t, event.SkillName, got.SkillName)
	}
}

func TestServer_Push_DisconnectedClientDoesNotPanic(t *testing.T) {
	srv, err := terminal.StartServer()
	require.NoError(t, err)
	defer srv.Stop()

	conn, _, err := websocket.DefaultDialer.Dial(
		wsAddr(srv.Port()),
		http.Header{"Origin": {"http://localhost"}},
	)
	require.NoError(t, err)
	conn.Close()
	time.Sleep(30 * time.Millisecond)

	assert.NotPanics(t, func() {
		srv.Push(terminal.RenderEvent{ID: "x", Ts: 1, Spec: terminal.RenderSpec{Type: "bar"}})
	})
}

func TestServer_Dashboard_ServedAtRoot(t *testing.T) {
	srv, err := terminal.StartServer()
	require.NoError(t, err)
	defer srv.Stop()

	resp, err := http.Get(httpAddr(srv.Port()))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestServer_WebSocket_RejectsNonLocalhost(t *testing.T) {
	srv, err := terminal.StartServer()
	require.NoError(t, err)
	defer srv.Stop()

	_, resp, _ := websocket.DefaultDialer.Dial(
		wsAddr(srv.Port()),
		http.Header{"Origin": {"http://evil.example.com"}},
	)
	if resp != nil {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
}
