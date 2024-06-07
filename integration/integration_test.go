package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration performs an integration test against an ephemeral Coder deployment.
// For each directory containing a `main.tf` under `/integration`, performs the following:
// - Creates a temporary Coder instance running in Docker
// - Runs the `main.tf` specified in the given test directory against the Coder deployment
// - Asserts the state of the deployment via `codersdk`
//
// TODO: currently all interfaces to this Coder deployment are currently performed using the CLI
// without github.com/coder/coder/v2/codersdk.
// We will need to be able to import codersdk in order for this provider to function.
func TestIntegration(t *testing.T) {
	if os.Getenv("TF_ACC") == "1" {
		t.Skip("Skipping integration tests during tf acceptance tests")
	}

	timeoutStr := os.Getenv("TIMEOUT_MINS")
	if timeoutStr == "" {
		timeoutStr = "10"
	}
	timeoutMins, err := strconv.Atoi(timeoutStr)
	require.NoError(t, err, "invalid value specified for timeout")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMins)*time.Minute)
	t.Cleanup(cancel)

	tfrcPath := setupProvider(ctx, t)

	for _, tt := range []struct {
		name string
		cmds map[string]string
	}{
		{
			name: "example-test",
			// TODO: replace this with a func(codersdk.Client)
			cmds: map[string]string{
				"coder_users_show_me": `coder users show me -o json`,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctrID := startCoder(ctx, t, tt.name)
			wd, err := os.Getwd()
			require.NoError(t, err)
			srcDir := filepath.Join(wd, tt.name)
			tfCmd := exec.CommandContext(ctx, "terraform", "-chdir="+srcDir, "apply", "-auto-approve")
			tfCmd.Env = append(tfCmd.Env, "TF_CLI_CONFIG_FILE="+tfrcPath)
			var buf bytes.Buffer
			tfCmd.Stdout = &buf
			tfCmd.Stderr = &buf
			if err := tfCmd.Run(); !assert.NoError(t, err) {
				t.Logf(buf.String())
			}
			for cmdKey, cmdVal := range tt.cmds {
				out, rc := execContainer(ctx, t, ctrID, cmdVal)
				require.Zero(t, rc)
				compareGoldenJSON(t, filepath.Join(wd, tt.name, cmdKey+"_golden.json"), out)
			}
		})
	}
}

// compareGoldenJSON reads the content of goldenFile, replaces variable strings
// in both the golden and actual content (like UUIDs and times) with placeholders
// before comparing the two as JSON.
func compareGoldenJSON(t testing.TB, goldenFile string, actual string) {
	goldenBytes, err := os.ReadFile(goldenFile)
	require.NoError(t, err)
	golden := replaceVariants(string(goldenBytes))
	actual = replaceVariants(actual)
	require.JSONEq(t, golden, actual)
}

var (
	exprUUID            = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)
	placeholderUUID     = "c0dec0dec0de-c0de-c0de-c0de-c0dec0dec0dec0de"
	exprDateTime        = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]+Z?`)
	placeholderDateTime = "1234-56-78T12:34:56:7890Z"
)

func replaceVariants(s string) string {
	s = exprUUID.ReplaceAllString(s, placeholderUUID)
	s = exprDateTime.ReplaceAllString(s, placeholderDateTime)
	return s
}

func setupProvider(ctx context.Context, t testing.TB) string {
	// Ensure the binary is built
	binPath, err := filepath.Abs("../terraform-provider-coderd")
	require.NoError(t, err)
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("not found: %q - please build the provider first", binPath)
	}
	// Create a terraformrc to point to our freshly built provider!
	cwd, err := os.Getwd()
	require.NoError(t, err)
	tfrcPath := filepath.Join(cwd, "integration.tfrc")
	provPath, err := filepath.Abs("..")
	require.NoError(t, err)
	testTerraformrc := fmt.Sprintf(`provider_installation {
dev_overrides {
	"coder/coderd" = %q
	}
	direct{}
}`, provPath)
	err = os.WriteFile(tfrcPath, []byte(testTerraformrc), 0o644)
	require.NoError(t, err, "write terraformrc to tempdir")
	t.Cleanup(func() {
		// _ = os.Remove(tfrcPath)
	})
	return tfrcPath
}

func startCoder(ctx context.Context, t testing.TB, name string) string {
	var (
		localURL = "http://localhost:3000"
	)

	coderImg := os.Getenv("CODER_IMAGE")
	if coderImg == "" {
		coderImg = "ghcr.io/coder/coder"
	}

	coderVersion := os.Getenv("CODER_VERSION")
	if coderVersion == "" {
		coderVersion = "latest"
	}

	t.Logf("using coder image %s:%s", coderImg, coderVersion)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "init docker client")

	// Stand up a temporary Coder instance
	ctr, err := cli.ContainerCreate(ctx, &container.Config{
		Image: coderImg + ":" + coderVersion,
		Env: []string{
			"CODER_ACCESS_URL=" + localURL, // Set explicitly to avoid creating try.coder.app URLs.
			"CODER_IN_MEMORY=true",         // We don't necessarily care about real persistence here.
			"CODER_TELEMETRY_ENABLE=false", // Avoid creating noise.
		},
		Labels: map[string]string{},
	}, nil, nil, nil, "terraform-provider-coderd-"+name)
	require.NoError(t, err, "create test deployment")

	t.Logf("created container %s\n", ctr.ID)
	t.Cleanup(func() { // Make sure we clean up after ourselves.
		// TODO: also have this execute if you Ctrl+C!
		t.Logf("stopping container %s\n", ctr.ID)
		_ = cli.ContainerRemove(ctx, ctr.ID, container.RemoveOptions{
			Force: true,
		})
	})

	err = cli.ContainerStart(ctx, ctr.ID, container.StartOptions{})
	require.NoError(t, err, "start container")
	t.Logf("started container %s\n", ctr.ID)

	// nolint:gosec // For testing only.
	var (
		testEmail    = "testing@coder.com"
		testPassword = "InsecurePassw0rd!"
		testUsername = "testing"
	)

	// Wait for container to come up
	require.Eventually(t, func() bool {
		_, rc := execContainer(ctx, t, ctr.ID, fmt.Sprintf(`curl -s --fail %s/api/v2/buildinfo`, localURL))
		if rc == 0 {
			return true
		}
		t.Logf("not ready yet...")
		return false
	}, 10*time.Second, time.Second, "coder failed to become ready in time")

	// Perform first time setup
	_, rc := execContainer(ctx, t, ctr.ID, fmt.Sprintf(`coder login %s --first-user-email=%q --first-user-password=%q --first-user-trial=false --first-user-username=%q`, localURL, testEmail, testPassword, testUsername))
	require.Equal(t, 0, rc, "failed to perform first-time setup")
	return ctr.ID
}

// execContainer executes the given command in the given container and returns
// the output and the exit code of the command.
func execContainer(ctx context.Context, t testing.TB, containerID, command string) (string, int) {
	t.Helper()
	t.Logf("exec container cmd: %q", command)
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "connect to docker")
	defer cli.Close()
	execConfig := types.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/bin/sh", "-c", command},
	}
	ex, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	require.NoError(t, err, "create container exec")
	resp, err := cli.ContainerExecAttach(ctx, ex.ID, types.ExecStartCheck{})
	require.NoError(t, err, "attach to container exec")
	defer resp.Close()
	var buf bytes.Buffer
	_, err = stdcopy.StdCopy(&buf, &buf, resp.Reader)
	require.NoError(t, err, "read stdout")
	out := buf.String()
	t.Log("exec container output:\n" + out)
	execResp, err := cli.ContainerExecInspect(ctx, ex.ID)
	require.NoError(t, err, "get exec exit code")
	return out, execResp.ExitCode
}
