package startup

import "testing"

func TestParseAgentWireGuardPort(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "fixed", value: "51820", want: 51820},
		{name: "lowest fixed", value: "1", want: 1},
		{name: "highest fixed", value: "65535", want: 65535},
		{name: "zero selects ephemeral", value: "0", want: -1},
		{name: "negative one selects ephemeral", value: "-1", want: -1},
		{name: "not a number", value: "abc", wantErr: true},
		{name: "negative out of range", value: "-2", wantErr: true},
		{name: "positive out of range", value: "65536", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentWireGuardPort(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAgentWireGuardPort(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("parseAgentWireGuardPort(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestResolveAgentWireGuardPortDefaultsToDiscoverablePort(t *testing.T) {
	got, err := resolveAgentWireGuardPort("")
	if err != nil {
		t.Fatalf("resolveAgentWireGuardPort(\"\") error = %v", err)
	}
	if got != 51820 {
		t.Fatalf("resolveAgentWireGuardPort(\"\") = %d, want 51820", got)
	}
}
