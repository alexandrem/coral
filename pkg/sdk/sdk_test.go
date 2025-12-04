package sdk

import (
	"log/slog"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				DebugAddr: ":9002",
				Logger:    slog.Default(),
			},
			wantErr: false,
		},
		{
			name: "default debug addr",
			config: Config{
				Logger: slog.Default(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk, err := New(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if sdk != nil {
				defer sdk.Close()
			}
		})
	}
}

func TestSDK_DebugAddr(t *testing.T) {
	t.Run("returns configured address", func(t *testing.T) {
		sdk, err := New(Config{
			DebugAddr: ":9090",
			Logger:    slog.Default(),
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer sdk.Close()

		if addr := sdk.DebugAddr(); addr != ":9090" {
			t.Errorf("DebugAddr() = %v, want :9090", addr)
		}
	})
}

func TestSDK_Close(t *testing.T) {
	sdk, err := New(Config{
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := sdk.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
