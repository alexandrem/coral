package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMeshSubnet(t *testing.T) {
	tests := []struct {
		name      string
		subnet    string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid CGNAT subnet",
			subnet:    "100.64.0.0/10",
			wantError: false,
		},
		{
			name:      "valid RFC 1918 /16",
			subnet:    "10.42.0.0/16",
			wantError: false,
		},
		{
			name:      "valid RFC 1918 /24",
			subnet:    "192.168.1.0/24",
			wantError: false,
		},
		{
			name:      "valid /12 subnet",
			subnet:    "172.16.0.0/12",
			wantError: false,
		},
		{
			name:      "empty subnet",
			subnet:    "",
			wantError: true,
			errorMsg:  "cannot be empty",
		},
		{
			name:      "invalid CIDR format",
			subnet:    "10.42.0.0",
			wantError: true,
			errorMsg:  "invalid",
		},
		{
			name:      "invalid CIDR",
			subnet:    "not-a-cidr",
			wantError: true,
			errorMsg:  "invalid",
		},
		{
			name:      "subnet too small (/25)",
			subnet:    "10.42.0.0/25",
			wantError: true,
			errorMsg:  "too small",
		},
		{
			name:      "subnet too small (/32)",
			subnet:    "10.42.0.0/32",
			wantError: true,
			errorMsg:  "too small",
		},
		{
			name:      "IPv6 subnet",
			subnet:    "fd42::/48",
			wantError: true,
			errorMsg:  "must be IPv4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipNet, err := ValidateMeshSubnet(tt.subnet)

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, ipNet)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, ipNet)
			}
		})
	}
}

func TestIsCGNATSubnet(t *testing.T) {
	tests := []struct {
		name     string
		subnet   string
		expected bool
	}{
		{
			name:     "CGNAT /10",
			subnet:   "100.64.0.0/10",
			expected: true,
		},
		{
			name:     "CGNAT /16 subset",
			subnet:   "100.64.0.0/16",
			expected: true,
		},
		{
			name:     "CGNAT /24 subset",
			subnet:   "100.100.0.0/24",
			expected: true,
		},
		{
			name:     "RFC 1918 10.x",
			subnet:   "10.42.0.0/16",
			expected: false,
		},
		{
			name:     "RFC 1918 192.168.x",
			subnet:   "192.168.1.0/24",
			expected: false,
		},
		{
			name:     "RFC 1918 172.16.x",
			subnet:   "172.16.0.0/12",
			expected: false,
		},
		{
			name:     "public IP range",
			subnet:   "8.8.8.0/24",
			expected: false,
		},
		{
			name:     "invalid subnet",
			subnet:   "invalid",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCGNATSubnet(tt.subnet)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSubnetCapacity(t *testing.T) {
	tests := []struct {
		name     string
		subnet   string
		expected int
		wantErr  bool
	}{
		{
			name:     "/10 CGNAT",
			subnet:   "100.64.0.0/10",
			expected: 4194302, // 2^22 - 2 (network + colony)
		},
		{
			name:     "/16 subnet",
			subnet:   "10.42.0.0/16",
			expected: 65534, // 2^16 - 2
		},
		{
			name:     "/24 subnet",
			subnet:   "192.168.1.0/24",
			expected: 254, // 2^8 - 2
		},
		{
			name:     "/8 subnet",
			subnet:   "10.0.0.0/8",
			expected: 16777214, // 2^24 - 2
		},
		{
			name:    "invalid subnet",
			subnet:  "invalid",
			wantErr: true,
		},
		{
			name:    "IPv6 subnet",
			subnet:  "fd42::/48",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capacity, err := SubnetCapacity(tt.subnet)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, capacity)
			}
		})
	}
}

func TestRecommendedMeshSubnets(t *testing.T) {
	subnets := RecommendedMeshSubnets()

	// Should have at least one recommendation
	assert.NotEmpty(t, subnets)

	// First recommendation should be CGNAT
	assert.Equal(t, "100.64.0.0/10", subnets[0])

	// All recommendations should be valid
	for _, subnet := range subnets {
		_, err := ValidateMeshSubnet(subnet)
		assert.NoError(t, err, "recommended subnet %s should be valid", subnet)
	}
}
