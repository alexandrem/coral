package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"strconv"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/cli/fleet"
	"github.com/coral-mesh/coral/internal/constants"
)

func bulkWatchRequested(cmd *cobra.Command, options fleet.Options) bool {
	if options.Selector.HasFilters() || options.All || options.DryRun || options.RetryFailed != "" || options.ReportFile != "" {
		return true
	}
	for _, flag := range []string{"concurrency", "retries", "request-timeout", "format"} {
		if cmd.Flags().Changed(flag) {
			return true
		}
	}
	return false
}

func runBulkWatch(cmd *cobra.Command, options *fleet.Options, colony string, specs []*types.ServiceSpec) error {
	requests := make([]*agentv1.ConnectServiceRequest, 0, len(specs))
	for _, spec := range specs {
		requests = append(requests, &agentv1.ConnectServiceRequest{Name: spec.Name, Port: spec.Port, HealthEndpoint: spec.HealthEndpoint, ServiceType: spec.ServiceType, ExePattern: spec.ExePattern, Labels: spec.Labels})
	}
	plan, err := options.Prepare(cmd.Context(), colony, "services watch", requests)
	if err != nil {
		return err
	}
	if !options.DryRun {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Watching %d services on %d pinned agents in colony %s\n", len(specs), len(plan.Targets), plan.ColonyID); err != nil {
			return err
		}
	}
	action := watchAction(requests, func(target fleet.Target) (agentv1connect.AgentServiceClient, error) {
		address := target.Agent.MeshIpv4
		if address == "" {
			address = target.Agent.MeshIpv6
		}
		if net.ParseIP(address) == nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("agent %s has no valid mesh address", target.ID))
		}
		endpoint := "http://" + net.JoinHostPort(address, strconv.Itoa(constants.DefaultAgentPort))
		return agentv1connect.NewAgentServiceClient(http.DefaultClient, endpoint), nil
	})
	return options.Finish(cmd, plan.Run(cmd.Context(), *options, action))
}

func watchSpecsFromReport(filename string) ([]*types.ServiceSpec, error) {
	report, err := fleet.LoadRetryReport(filename, "services watch")
	if err != nil {
		return nil, err
	}
	var requests []*agentv1.ConnectServiceRequest
	if err := json.Unmarshal(report.Parameters, &requests); err != nil {
		return nil, fmt.Errorf("invalid service specifications in retry report: %w", err)
	}
	specs := make([]*types.ServiceSpec, 0, len(requests))
	for _, request := range requests {
		if request == nil {
			return nil, fmt.Errorf("retry report contains an empty service specification")
		}
		if !types.IsValidServiceName(request.Name) || request.Port < 0 || request.Port > 65535 {
			return nil, fmt.Errorf("invalid service name or port in retry report")
		}
		specs = append(specs, &types.ServiceSpec{Name: request.Name, Port: request.Port, HealthEndpoint: request.HealthEndpoint, ServiceType: request.ServiceType, ExePattern: request.ExePattern, Labels: request.Labels})
	}
	return specs, types.ValidateServiceSpecs(specs)
}

type watchedService struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type watchProgress struct {
	Services []watchedService `json:"services"`
}

// watchAction retains successful registrations across target retries and reports conflicts
// instead of letting ConnectService's existing AlreadyExists handling imply a config update.
func watchAction(requests []*agentv1.ConnectServiceRequest, clientFor func(fleet.Target) (agentv1connect.AgentServiceClient, error)) fleet.Action {
	return func(ctx context.Context, target fleet.Target, previous json.RawMessage) (string, json.RawMessage, error) {
		progress := watchProgress{Services: make([]watchedService, len(requests))}
		if len(previous) > 0 {
			if err := json.Unmarshal(previous, &progress); err != nil {
				return "", previous, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid prior service results: %w", err))
			}
			if len(progress.Services) != len(requests) {
				return "", previous, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("prior service result count differs from requested services"))
			}
		}
		for i, request := range requests {
			if progress.Services[i].Name != "" && progress.Services[i].Name != request.Name {
				return "", previous, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("prior service results differ from requested services"))
			}
			progress.Services[i].Name = request.Name
		}
		client, err := clientFor(target)
		if err != nil {
			return "", previous, err
		}
		inventory, err := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
		if err != nil {
			return "", previous, err
		}
		var failures []error
		transient := false
		completed := 0
		for i, request := range requests {
			result := &progress.Services[i]
			if result.Status == "connected" || result.Status == "unchanged" {
				completed++
				continue
			}
			if target.Attempt > 1 && result.Status == "failed" && !result.Retryable {
				failures = append(failures, fmt.Errorf("%s: %s", request.Name, result.Error))
				continue
			}
			unchanged, err := checkRegistration(request, inventory.Msg.Services)
			if err == nil && unchanged {
				result.Status, result.Error = "unchanged", ""
				result.Retryable = false
				completed++
				continue
			}
			if err == nil {
				resp, callErr := client.ConnectService(ctx, connect.NewRequest(request))
				err = callErr
				if err == nil && !resp.Msg.Success {
					message := resp.Msg.Error
					if message == "" {
						message = "agent rejected service registration"
					}
					err = connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s", message))
				}
				if err == nil {
					// The existing RPC can return success for AlreadyExists. Verify what
					// is actually registered before declaring this configuration applied.
					latest, readErr := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
					err = readErr
					if err == nil {
						inventory = latest
						var registered bool
						registered, err = checkRegistration(request, inventory.Msg.Services)
						if err == nil && !registered {
							err = connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("agent acknowledged %s but the requested registration is not present", request.Name))
						}
					}
				}
			}
			if err != nil {
				result.Status, result.Error = "failed", err.Error()
				result.Retryable = fleet.Retryable(err)
				failures = append(failures, fmt.Errorf("%s: %w", request.Name, err))
				transient = transient || fleet.Retryable(err)
			} else {
				result.Status, result.Error = "connected", ""
				result.Retryable = false
				completed++
			}
		}
		data, err := json.Marshal(progress)
		if err != nil {
			return "", nil, err
		}
		message := fmt.Sprintf("%d/%d services registered", completed, len(requests))
		if len(failures) > 0 {
			code := connect.CodeFailedPrecondition
			if transient {
				code = connect.CodeUnavailable
			}
			return message, data, connect.NewError(code, errors.Join(failures...))
		}
		return message, data, nil
	}
}

func checkRegistration(request *agentv1.ConnectServiceRequest, services []*agentv1.ServiceStatus) (bool, error) {
	for _, service := range services {
		if service.NamingSource != agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE {
			continue
		}
		if service.Name != request.Name && (request.Port == 0 || service.Port != request.Port) {
			continue
		}
		if service.Name != request.Name || service.Port != request.Port || service.HealthEndpoint != request.HealthEndpoint || service.ExePattern != request.ExePattern || (service.HasMonitor && (service.ServiceType != request.ServiceType || !maps.Equal(service.Labels, request.Labels))) {
			return false, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("existing authoritative registration %s conflicts with requested %s; update the agent's service configuration", service.Name, request.Name))
		}
		return true, nil
	}
	return false, nil
}
