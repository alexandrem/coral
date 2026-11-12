package colony

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/config"
)

func setupExportColony(t *testing.T) (string, string, *ca.InitResult) {
	t.Helper()
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Chdir(t.TempDir())

	const colonyID = "export-test-colony"
	loader, err := config.NewLoader()
	require.NoError(t, err)
	cfg := config.DefaultColonyConfig(colonyID, "export-test", "dev")
	cfg.StoragePath = t.TempDir()
	require.NoError(t, loader.SaveColonyConfig(cfg))

	caDir := filepath.Join(loader.ColonyDir(colonyID), "ca")
	result, err := ca.Initialize(caDir, colonyID)
	require.NoError(t, err)
	return colonyID, caDir, result
}

func runExport(t *testing.T, colonyID, format string) (string, error) {
	t.Helper()
	cmd := newExportCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	args := []string{"--format", format}
	if colonyID != "" {
		args = append(args, colonyID)
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

func TestExportAgentCredentials(t *testing.T) {
	colonyID, _, initialized := setupExportColony(t)

	for _, state := range []string{"freshly initialized", "rotated"} {
		t.Run(state, func(t *testing.T) {
			psk := initialized.BootstrapPSK
			if state == "rotated" {
				manager, db, _, err := helpers.GetCAManager(colonyID)
				require.NoError(t, err)
				psk, err = manager.RotatePSK(context.Background(), time.Hour)
				require.NoError(t, err)
				require.NoError(t, db.Close())
				require.NotEqual(t, initialized.BootstrapPSK, psk)
			}

			for _, format := range []string{"env", "yaml", "json", "k8s"} {
				t.Run(format, func(t *testing.T) {
					output, err := runExport(t, colonyID, format)
					require.NoError(t, err)
					values := map[string]string{}
					switch format {
					case "env":
						for _, line := range strings.Split(output, "\n") {
							if assignment, ok := strings.CutPrefix(line, "export CORAL_"); ok {
								key, value, _ := strings.Cut(assignment, "=")
								values[strings.ToLower(key)] = strings.Trim(value, "\"")
							}
						}
					case "yaml":
						require.NoError(t, yaml.Unmarshal([]byte(output), &values))
					case "json":
						require.NoError(t, json.Unmarshal([]byte(output), &values))
					case "k8s":
						var secret struct {
							Kind       string            `yaml:"kind"`
							StringData map[string]string `yaml:"stringData"`
						}
						require.NoError(t, yaml.Unmarshal([]byte(output), &secret))
						require.Equal(t, "Secret", secret.Kind)
						for key, value := range secret.StringData {
							values[strings.ReplaceAll(key, "-", "_")] = value
						}
					}
					require.Len(t, values, 4)
					require.Equal(t, colonyID, values["colony_id"])
					require.Equal(t, "sha256:"+initialized.RootFingerprint, values["ca_fingerprint"])
					require.Equal(t, config.DefaultGlobalConfig().Discovery.Endpoint, values["discovery_endpoint"])
					require.Equal(t, psk, values["bootstrap_psk"])
				})
			}
		})
	}
}

func TestExportMissingPSKProducesNoOutput(t *testing.T) {
	colonyID, caDir, _ := setupExportColony(t)
	require.NoError(t, os.Remove(filepath.Join(caDir, "bootstrap-psk.enc")))

	for _, format := range []string{"env", "yaml", "json", "k8s"} {
		t.Run(format, func(t *testing.T) {
			output, err := runExport(t, colonyID, format)
			require.ErrorContains(t, err, "no active PSK found")
			require.Empty(t, output)
		})
	}
}

func TestExportCurrentColony(t *testing.T) {
	colonyID, _, initialized := setupExportColony(t)
	t.Setenv("CORAL_COLONY_ID", colonyID)
	output, err := runExport(t, "", "json")
	require.NoError(t, err)
	var values map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &values))
	require.Equal(t, colonyID, values["colony_id"])
	require.Equal(t, initialized.BootstrapPSK, values["bootstrap_psk"])
}

func TestExportUnknownColonyDoesNotCreateCA(t *testing.T) {
	colonyID, _, _ := setupExportColony(t)
	t.Setenv("CORAL_COLONY_ID", colonyID)

	// A project storage override must not cause an unknown colony's CA to be
	// paired with the current colony's database.
	require.NoError(t, config.SaveProjectConfig(".", &config.ProjectConfig{
		Version: config.SchemaVersion, ColonyID: colonyID,
	}))
	output, err := runExport(t, "env", "env")
	require.ErrorContains(t, err, `failed to load colony "env"`)
	require.Empty(t, output)
	loader, err := config.NewLoader()
	require.NoError(t, err)
	_, err = os.Stat(loader.ColonyDir("env"))
	require.True(t, os.IsNotExist(err))
}

type failingExportWriter struct {
	err error
}

func (w failingExportWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestExportPropagatesOutputError(t *testing.T) {
	colonyID, _, _ := setupExportColony(t)
	writeErr := errors.New("output unavailable")

	for _, format := range []string{"env", "yaml", "json", "k8s"} {
		t.Run(format, func(t *testing.T) {
			cmd := newExportCmd()
			cmd.SetOut(failingExportWriter{err: writeErr})
			cmd.SetArgs([]string{colonyID, "--format", format})
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			err := cmd.Execute()
			require.ErrorIs(t, err, writeErr)
			require.ErrorContains(t, err, "failed to write agent enrollment credentials")
		})
	}
}
