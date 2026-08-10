package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony/enrollment"
)

// TestBootstrapAndRegister_UnimplementedWithoutEnroller and
// TestBootstrapAndRegister_PermissionDeniedWithoutRecordIDHeader are the
// Server-level half of RFD 109's "No broad rendezvous handler" guarantee:
// even if BootstrapAndRegister somehow reached this handler, it refuses to
// process anything that didn't arrive with the trusted record_id header the
// rendezvous dialer sets after its own nonce check succeeds.
func TestBootstrapAndRegister_UnimplementedWithoutEnroller(t *testing.T) {
	srv, cleanup := newTestServer(t, Config{ColonyID: "colony-1"})
	defer cleanup()

	_, err := srv.BootstrapAndRegister(context.Background(), connect.NewRequest(&colonyv1.BootstrapAndRegisterRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestBootstrapAndRegister_PermissionDeniedWithoutRecordIDHeader(t *testing.T) {
	srv, cleanup := newTestServer(t, Config{ColonyID: "colony-1"})
	defer cleanup()

	// A non-nil enroller with no record_id header must still be rejected —
	// the header is the only thing distinguishing a rendezvous dial-back
	// call from a direct call that reached this handler some other way.
	// Sub-dependencies stay nil deliberately: this must never be invoked,
	// since recordID=="" is checked before the enroller is ever called.
	srv.SetEnroller(enrollment.NewEnroller(nil, nil, nil, nil, nil, nil, nil, zerolog.Nop()))

	req := connect.NewRequest(&colonyv1.BootstrapAndRegisterRequest{})
	_, err := srv.BootstrapAndRegister(context.Background(), req)
	require.Error(t, err)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
