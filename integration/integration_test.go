package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration performs an integration test against an ephemeral Coder deployment.
// For each directory containing a `main.tf` under `/integration`, performs the following:
// - Creates a temporary Coder instance running in Docker
// - Runs the `main.tf` specified in the given test directory against the Coder deployment
// - Asserts the state of the deployment via `codersdk`.
func TestIntegration(t *testing.T) {
	if os.Getenv("TF_ACC") == "1" {
		t.Skip("Skipping integration tests during tf acceptance tests")
	}
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	timeoutStr := os.Getenv("TIMEOUT_MINS")
	if timeoutStr == "" {
		timeoutStr = "10"
	}
	timeoutMins, err := strconv.Atoi(timeoutStr)
	require.NoError(t, err, "invalid value specified for timeout")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMins)*time.Minute)
	t.Cleanup(cancel)

	tfrcPath := setupProvider(t)

	for _, tt := range []struct {
		name    string
		preF    func(testing.TB, *codersdk.Client)
		assertF func(testing.TB, *codersdk.Client)
	}{
		{
			name: "user-test",
			preF: func(t testing.TB, c *codersdk.Client) {
				me, err := c.User(ctx, codersdk.Me)
				require.NoError(t, err)
				c.CreateUser(ctx, codersdk.CreateUserRequest{
					Email:          "test2@coder.com",
					Username:       "ethan",
					Password:       "SomeSecurePassword!",
					UserLoginType:  "password",
					DisableLogin:   false,
					OrganizationID: me.OrganizationIDs[0],
				})
			},
			assertF: func(t testing.TB, c *codersdk.Client) {
				// Check user fields.
				user, err := c.User(ctx, "dean")
				assert.NoError(t, err)
				assert.Equal(t, "dean", user.Username)
				assert.Equal(t, "Dean Coolguy", user.Name)
				assert.Equal(t, "test@coder.com", user.Email)
				roles := make([]string, len(user.Roles))
				for i, role := range user.Roles {
					roles[i] = role.Name
				}
				assert.ElementsMatch(t, []string{"owner", "template-admin"}, roles)
				assert.Equal(t, codersdk.LoginTypePassword, user.LoginType)
				assert.Contains(t, []codersdk.UserStatus{codersdk.UserStatusActive, codersdk.UserStatusDormant}, user.Status)

				// Test password.
				newClient := codersdk.New(c.URL)
				res, err := newClient.LoginWithPassword(ctx, codersdk.LoginWithPasswordRequest{
					Email:    "test@coder.com",
					Password: "SomeSecurePassword!",
				})
				assert.NoError(t, err)
				newClient.SetSessionToken(res.SessionToken)
				user, err = newClient.User(ctx, codersdk.Me)
				assert.NoError(t, err)
				assert.Equal(t, "dean", user.Username)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := startCoder(ctx, t, tt.name)
			wd, err := os.Getwd()
			require.NoError(t, err)
			srcDir := filepath.Join(wd, tt.name)
			// Delete all .tfstate files
			err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
				if filepath.Ext(path) == ".tfstate" {
					return os.Remove(path)
				}
				return nil
			})
			require.NoError(t, err)
			tfCmd := exec.CommandContext(ctx, "terraform", "-chdir="+srcDir, "apply", "-auto-approve")
			tfCmd.Env = append(tfCmd.Env, "TF_CLI_CONFIG_FILE="+tfrcPath)
			tfCmd.Env = append(tfCmd.Env, "CODER_URL="+client.URL.String())
			tfCmd.Env = append(tfCmd.Env, "CODER_SESSION_TOKEN="+client.SessionToken())
			var buf bytes.Buffer
			tfCmd.Stdout = &buf
			tfCmd.Stderr = &buf
			tt.preF(t, client)
			if err := tfCmd.Run(); !assert.NoError(t, err) {
				t.Logf(buf.String())
			}
			tt.assertF(t, client)
		})
	}
}

func setupProvider(t testing.TB) string {
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

func startCoder(ctx context.Context, t *testing.T, name string) *codersdk.Client {
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

	p := randomPort(t)
	t.Logf("random port is %d", p)
	// Stand up a temporary Coder instance
	puller, err := cli.ImagePull(ctx, coderImg+":"+coderVersion, image.PullOptions{})
	require.NoError(t, err, "pull coder image")
	defer puller.Close()
	_, err = io.Copy(os.Stderr, puller)
	require.NoError(t, err, "pull coder image")
	ctr, err := cli.ContainerCreate(ctx, &container.Config{
		Image: coderImg + ":" + coderVersion,
		Env: []string{
			"CODER_HTTP_ADDRESS=0.0.0.0:3000",        // Listen on all interfaces inside the container
			"CODER_ACCESS_URL=http://localhost:3000", // Set explicitly to avoid creating try.coder.app URLs.
			"CODER_IN_MEMORY=true",                   // We don't necessarily care about real persistence here.
			"CODER_TELEMETRY_ENABLE=false",           // Avoid creating noise.
		},
		Labels:       map[string]string{},
		ExposedPorts: map[nat.Port]struct{}{nat.Port("3000/tcp"): {}},
	}, &container.HostConfig{
		PortBindings: map[nat.Port][]nat.PortBinding{
			nat.Port("3000/tcp"): {{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", p)}},
		},
	}, nil, nil, "terraform-provider-coderd-"+name)
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

	// Perform first time setup
	coderURL, err := url.Parse(fmt.Sprintf("http://localhost:%d", p))
	require.NoError(t, err, "parse coder URL")
	client := codersdk.New(coderURL)
	// Wait for container to come up
	require.Eventually(t, func() bool {
		_, err := client.BuildInfo(ctx)
		if err != nil {
			t.Logf("not ready yet: %s", err.Error())
		}
		return err == nil
	}, 10*time.Second, time.Second, "coder failed to become ready in time")
	_, err = client.CreateFirstUser(ctx, codersdk.CreateFirstUserRequest{
		Email:    testEmail,
		Username: testUsername,
		Password: testPassword,
	})
	require.NoError(t, err, "create first user")
	resp, err := client.LoginWithPassword(ctx, codersdk.LoginWithPasswordRequest{
		Email:    testEmail,
		Password: testPassword,
	})
	require.NoError(t, err, "login to coder instance with password")
	client.SetSessionToken(resp.SessionToken)
	return client
}

// randomPort is a helper function to find a free random port.
// Note that the OS may reallocate the port very quickly, so
// this is not _guaranteed_.
func randomPort(t *testing.T) int {
	random, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "failed to listen on localhost")
	_ = random.Close()
	tcpAddr, valid := random.Addr().(*net.TCPAddr)
	require.True(t, valid, "random port address is not a *net.TCPAddr?!")
	return tcpAddr.Port
}
