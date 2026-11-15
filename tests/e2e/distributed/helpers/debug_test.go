package helpers

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

type attachUprobeClient struct {
	colonyv1connect.ColonyDebugServiceClient
	response *colonyv1.AttachUprobeResponse
	err      error
}

func (c attachUprobeClient) AttachUprobe(context.Context, *connect.Request[colonyv1.AttachUprobeRequest]) (*connect.Response[colonyv1.AttachUprobeResponse], error) {
	return connect.NewResponse(c.response), c.err
}

func TestAttachUprobeHelpers(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		for _, tc := range []struct {
			name     string
			response *colonyv1.AttachUprobeResponse
			err      error
			wantErr  string
		}{
			{name: "success", response: &colonyv1.AttachUprobeResponse{Success: true, SessionId: "session-1"}},
			{name: "application failure", response: &colonyv1.AttachUprobeResponse{Error: "agent failed to start collector"}, wantErr: "agent failed to start collector"},
			{name: "transport failure", err: errors.New("connection refused"), wantErr: "connection refused"},
		} {
			name := tc.name
			if filtered {
				name += " with filter"
			}
			t.Run(name, func(t *testing.T) {
				client := attachUprobeClient{response: tc.response, err: tc.err}
				var resp *colonyv1.AttachUprobeResponse
				var err error
				if filtered {
					resp, err = AttachUprobeWithFilter(context.Background(), client, "agent-1", "sdk-app", "main.ProcessPayment", 30, nil)
				} else {
					resp, err = AttachUprobe(context.Background(), client, "agent-1", "sdk-app", "main.ProcessPayment", 30)
				}
				if tc.wantErr != "" {
					require.ErrorContains(t, err, tc.wantErr)
					require.Nil(t, resp)
					return
				}
				require.NoError(t, err)
				require.Equal(t, "session-1", resp.SessionId)
			})
		}
	}
}
