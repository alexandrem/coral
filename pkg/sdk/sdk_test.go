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
			name: "valid config with debug disabled",
			config: Config{
				ServiceName: "test-service",
				EnableDebug: false,
				Logger:      slog.Default(),
			},
			wantErr: false,
		},
		{
			name: "missing service name",
			config: Config{
				EnableDebug: false,
				Logger:      slog.Default(),
			},
			wantErr: true,
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
	t.Run("debug disabled", func(t *testing.T) {
		sdk, err := New(Config{
			ServiceName: "test-service",
			EnableDebug: false,
			Logger:      slog.Default(),
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer sdk.Close()

		if addr := sdk.DebugAddr(); addr != "" {
			t.Errorf("DebugAddr() = %v, want empty string when debug disabled", addr)
		}
	})
}

func TestSDK_Close(t *testing.T) {
	sdk, err := New(Config{
		ServiceName: "test-service",
		EnableDebug: false,
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := sdk.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
