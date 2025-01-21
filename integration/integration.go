package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/stretchr/testify/require"
)

func StartCoder(ctx context.Context, t *testing.T, name string, useLicense bool) *codersdk.Client {
	coderImg := os.Getenv("CODER_IMAGE")
	if coderImg == "" {
		coderImg = "ghcr.io/coder/coder"
	}

	coderVersion := os.Getenv("CODER_VERSION")
	if coderVersion == "" {
		coderVersion = "latest"
	}

	coderLicense := os.Getenv("CODER_ENTERPRISE_LICENSE")
	if useLicense && coderLicense == "" {
		t.Skip("Skipping tests that require a license.")
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
			"CODER_HTTP_ADDRESS=0.0.0.0:3000",          // Listen on all interfaces inside the container
			"CODER_ACCESS_URL=http://localhost:3000",   // Set explicitly to avoid creating try.coder.app URLs.
			"CODER_TELEMETRY_ENABLE=false",             // Avoid creating noise.
			"CODER_DANGEROUS_DISABLE_RATE_LIMITS=true", // Avoid hitting file rate limit in tests.
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
		testEmail    = "admin@coder.com"
		testPassword = "InsecurePassw0rd!"
		testUsername = "admin"
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
	}, 20*time.Second, time.Second, "coder failed to become ready in time")
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
	if useLicense {
		_, err := client.AddLicense(ctx, codersdk.AddLicenseRequest{
			License: coderLicense,
		})
		require.NoError(t, err, "add license")
	}
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
