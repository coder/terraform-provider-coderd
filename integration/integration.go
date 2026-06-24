package integration

import (
	"bytes"
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
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"

	"github.com/stretchr/testify/require"
)

// User-configurable options for coder backend.
// Using the pattern from
// https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis
type coderOptions struct {
	useLicense       bool
	enableRateLimits bool
	image            string
	version          string
	experiments      string
}

func UseLicense(opts *coderOptions) {
	opts.useLicense = true
}
func CoderImage(image string) func(opts *coderOptions) {
	return func(opts *coderOptions) {
		opts.image = image
	}
}
func CoderVersion(version string) func(opts *coderOptions) {
	return func(opts *coderOptions) {
		opts.version = version
	}
}
func CoderExperiments(experiments string) func(opts *coderOptions) {
	return func(opts *coderOptions) {
		opts.experiments = experiments
	}
}

func EnableRateLimits(opts *coderOptions) {
	opts.enableRateLimits = true
}

func StartCoder(ctx context.Context, t *testing.T, name string, options ...func(*coderOptions)) *codersdk.Client {
	// Start with the defaults.
	opts := coderOptions{
		useLicense:  false,
		image:       "ghcr.io/coder/coder",
		version:     "latest",
		experiments: "",
	}

	// Apply user-selected options.
	for _, option := range options {
		option(&opts)
	}

	if v, ok := os.LookupEnv("CODER_IMAGE"); ok {
		opts.image = v
	}
	if v, ok := os.LookupEnv("CODER_VERSION"); ok {
		opts.version = v
	}
	if v, ok := os.LookupEnv("CODER_EXPERIMENTS"); ok {
		opts.experiments = v
	}

	coderLicense := os.Getenv("CODER_ENTERPRISE_LICENSE")
	if opts.useLicense && coderLicense == "" {
		t.Skip("Skipping tests that require a license.")
	}

	ref := opts.image + ":" + opts.version
	t.Logf("using coder image %s:%s", opts.image, opts.version)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "init docker client")

	p := randomPort(t)
	t.Logf("random port is %d", p)

	// Give Coder an external PostgreSQL on a per-test network instead of its
	// embedded one, which downloads a binary from Maven at startup (a flaky,
	// rate-limited fetch that reds CI lanes).
	netName := "terraform-provider-coderd-" + name + "-net"
	net, err := cli.NetworkCreate(ctx, netName, network.CreateOptions{})
	require.NoError(t, err, "create test network")
	t.Cleanup(func() {
		// t.Context() is canceled before t.Cleanup callbacks run, so use a
		// fresh context or the removal is a no-op and the network leaks.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := cli.NetworkRemove(ctx, net.ID); err != nil {
			t.Logf("error removing network %s: %v", net.ID, err)
		}
	})
	startPostgres(ctx, t, cli, netName)

	// Stand up a temporary Coder instance.
	pullImage(ctx, t, cli, ref)

	env := []string{
		"CODER_HTTP_ADDRESS=0.0.0.0:3000",        // Listen on all interfaces inside the container.
		"CODER_ACCESS_URL=http://localhost:3000", // Avoid creating try.coder.app URLs.
		"CODER_TELEMETRY_ENABLE=false",           // Avoid creating noise.
		"CODER_PG_CONNECTION_URL=postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable",
	}
	if !opts.enableRateLimits {
		env = append(env, "CODER_DANGEROUS_DISABLE_RATE_LIMITS=true")
	}
	if opts.experiments != "" {
		env = append(env, "CODER_EXPERIMENTS="+opts.experiments)
	}

	ctr, err := cli.ContainerCreate(ctx, &container.Config{
		Image:        ref,
		Env:          env,
		Labels:       map[string]string{},
		ExposedPorts: map[nat.Port]struct{}{nat.Port("3000/tcp"): {}},
	}, &container.HostConfig{
		PortBindings: map[nat.Port][]nat.PortBinding{
			nat.Port("3000/tcp"): {{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", p)}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{netName: {}},
	}, nil, "terraform-provider-coderd-"+name)
	require.NoError(t, err, "create test deployment")

	t.Logf("created container %s\n", ctr.ID)
	t.Cleanup(func() { // Make sure we clean up after ourselves.
		// TODO: also have this execute if you Ctrl+C!
		t.Logf("stopping container %s\n", ctr.ID)
		// t.Context() is canceled before t.Cleanup callbacks run, so use a
		// fresh context or the removal is a no-op and the container leaks.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = cli.ContainerRemove(ctx, ctr.ID, container.RemoveOptions{
			Force: true,
		})
	})
	t.Cleanup(func() {
		if t.Failed() {
			dumpContainerLogs(t, cli, ctr.ID)
		}
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
	// Wait for the container to come up.
	require.Eventually(t, func() bool {
		_, err := client.BuildInfo(ctx)
		if err != nil {
			t.Logf("not ready yet: %s", err.Error())
		}
		return err == nil
	}, 90*time.Second, time.Second, "coder failed to become ready in time")
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
	if opts.useLicense {
		_, err := client.AddLicense(ctx, codersdk.AddLicenseRequest{
			License: coderLicense,
		})
		require.NoError(t, err, "add license")
	}
	return client
}

// pullImage pulls a Docker image, streaming progress to stderr.
func pullImage(ctx context.Context, t *testing.T, cli *client.Client, ref string) {
	t.Helper()
	reader, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	require.NoError(t, err, "pull image %s", ref)
	defer func() {
		if err := reader.Close(); err != nil {
			t.Logf("error closing image puller: %v", err)
		}
	}()
	_, err = io.Copy(os.Stderr, reader)
	require.NoError(t, err, "read image pull output for %s", ref)
}

// startPostgres runs a throwaway PostgreSQL that Coder connects to instead of
// starting its embedded one. It's reachable at hostname "postgres" on netName.
// Coder retries its database connection on startup, so we don't wait for it.
func startPostgres(ctx context.Context, t *testing.T, cli *client.Client, netName string) {
	t.Helper()
	const ref = "us-docker.pkg.dev/coder-v2-images-public/public/postgres:17"
	pullImage(ctx, t, cli, ref)
	ctr, err := cli.ContainerCreate(ctx, &container.Config{
		Image: ref,
		Env:   []string{"POSTGRES_PASSWORD=postgres"},
	}, &container.HostConfig{}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {Aliases: []string{"postgres"}},
		},
	}, nil, "")
	require.NoError(t, err, "create postgres container")
	t.Cleanup(func() {
		// t.Context() is canceled before t.Cleanup callbacks run, so use a
		// fresh context or the removal is a no-op and the container leaks.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = cli.ContainerRemove(ctx, ctr.ID, container.RemoveOptions{Force: true})
	})
	require.NoError(t, cli.ContainerStart(ctx, ctr.ID, container.StartOptions{}), "start postgres container")
}

func dumpContainerLogs(t *testing.T, cli *client.Client, containerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logs, err := cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		t.Logf("failed to fetch logs for container %s: %v", containerID, err)
		return
	}
	defer func() {
		if err := logs.Close(); err != nil {
			t.Logf("error closing container log reader: %v", err)
		}
	}()
	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, logs); err != nil {
		t.Logf("failed to read logs for container %s: %v", containerID, err)
		return
	}
	t.Logf("=== coder container %s logs ===\n%s=== end coder container logs ===", containerID, buf.String())
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
