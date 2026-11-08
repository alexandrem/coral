package distributed

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIExecSuite exercises container selection through the CLI and the agent's
// real nsenter execution. The apps share agent-0's PID namespace but have
// separate mount namespaces.
type CLIExecSuite struct {
	E2EDistributedSuite

	cliEnv    *helpers.CLITestEnv
	agentAddr string
}

func (s *CLIExecSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err)
	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", colonyEndpoint)
	s.Require().NoError(err)
	s.agentAddr, err = s.fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err)
}

func (s *CLIExecSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

func (s *CLIExecSuite) dockerOutput(service string, command ...string) string {
	s.T().Helper()
	args := append([]string{"exec", fmt.Sprintf("coral-e2e-%s-1", service)}, command...)
	output, err := exec.CommandContext(s.ctx, "docker", args...).CombinedOutput()
	s.Require().NoError(err, "docker exec %s: %s", service, output)
	return strings.TrimSpace(string(output))
}

func (s *CLIExecSuite) execContainer(selector string, command ...string) *helpers.CLIResult {
	s.T().Helper()
	// Use the mapped agent endpoint because mesh addresses are not reachable
	// from every test runner. No service registration is needed for this path.
	args := []string{"exec", "otel-app", "--agent-addr", s.agentAddr, "--container", selector, "--"}
	return s.cliEnv.Run(s.ctx, append(args, command...)...)
}

// TestContainerExecProcessName verifies the selected namespace itself rather
// than accepting arbitrary output from a command run in the wrong container.
func (s *CLIExecSuite) TestContainerExecProcessName() {
	agentMount := s.dockerOutput("agent-0", "readlink", "/proc/self/ns/mnt")
	for _, service := range []string{"cpu-app", "otel-app"} {
		s.Run(service, func() {
			wantMount := s.dockerOutput(service, "readlink", "/proc/self/ns/mnt")
			s.Require().NotEqual(agentMount, wantMount, "fixture must have distinct mount namespaces")

			result := s.execContainer(service, "readlink", "/proc/self/ns/mnt")
			result.MustSucceed(s.T())
			s.Equal(wantMount, strings.TrimSpace(result.Output), "exec must enter %s's mount namespace", service)
		})
	}
}

// TestContainerExecPID verifies that a PID selector uses the PID visible to
// the agent, including when the target is not PID 1 in the shared namespace.
func (s *CLIExecSuite) TestContainerExecPID() {
	pid := s.dockerOutput("agent-0", "pgrep", "-x", "otel-app")
	n, err := strconv.Atoi(pid)
	s.Require().NoError(err, "expected a single otel-app process")
	s.Require().Greater(n, 1)
	wantMount := s.dockerOutput("otel-app", "readlink", "/proc/self/ns/mnt")

	result := s.execContainer("pid:"+pid, "readlink", "/proc/self/ns/mnt")
	result.MustSucceed(s.T())
	s.Equal(wantMount, strings.TrimSpace(result.Output))
}

// TestContainerExecRejectsInvalidTargets verifies current failed_precondition
// errors reach the CLI instead of silently executing against a fallback PID.
func (s *CLIExecSuite) TestContainerExecRejectsInvalidTargets() {
	for _, tt := range []struct {
		name     string
		selector string
		wantErr  string
	}{
		{"missing process", "definitely-not-a-real-container-xyzzy", "not found among visible processes"},
		{"partial process name", "otel-ap", "not found among visible processes"},
		{"invalid PID", "pid:0", "expected pid:<positive integer>"},
		{"invalid container ID", "id:nginx", "expected id:<hex prefix>"},
	} {
		s.Run(tt.name, func() {
			result := s.execContainer(tt.selector, "readlink", "/proc/self/ns/mnt")
			result.MustFail(s.T())
			s.NotZero(result.ExitCode, "CLI must exit unsuccessfully")
			s.Contains(result.Output, "failed_precondition")
			s.Contains(result.Output, tt.wantErr)
		})
	}
}
