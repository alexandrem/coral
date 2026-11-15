package colony

import (
	"reflect"
	"testing"
)

func TestNewMCPCmdExposesCanonicalCommands(t *testing.T) {
	cmd := NewMCPCmd()

	for _, name := range []string{"configure", "list-tools", "proxy"} {
		if child, _, err := cmd.Find([]string{name}); err != nil || child == cmd {
			t.Fatalf("expected coral mcp %s to be registered", name)
		}
	}

	for _, legacyName := range []string{"generate-config", "test-tool"} {
		if child, _, _ := cmd.Find([]string{legacyName}); child != cmd {
			t.Fatalf("did not expect %s on the public coral mcp command", legacyName)
		}
	}
}

func TestLegacyColonyMCPCommandRemainsHidden(t *testing.T) {
	cmd := newMCPCmd()
	if !cmd.Hidden {
		t.Fatal("expected coral colony mcp compatibility command to be hidden")
	}

	for _, name := range []string{"generate-config", "proxy"} {
		if child, _, err := cmd.Find([]string{name}); err != nil || child == cmd {
			t.Fatalf("expected legacy coral colony mcp %s to remain registered", name)
		}
	}
}

func TestMCPClientConfigUsesCanonicalProxyPath(t *testing.T) {
	tests := []struct {
		name     string
		colonies []string
		want     map[string]interface{}
	}{
		{
			name:     "single colony",
			colonies: []string{"production"},
			want: map[string]interface{}{"mcpServers": map[string]interface{}{
				"coral": map[string]interface{}{"command": "coral", "args": []string{"mcp", "proxy"}},
			}},
		},
		{
			name:     "multiple colonies",
			colonies: []string{"production", "staging"},
			want: map[string]interface{}{"mcpServers": map[string]interface{}{
				"coral-production": map[string]interface{}{"command": "coral", "args": []string{"mcp", "proxy", "--colony", "production"}},
				"coral-staging":    map[string]interface{}{"command": "coral", "args": []string{"mcp", "proxy", "--colony", "staging"}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpClientConfig(tt.colonies); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mcpClientConfig() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
