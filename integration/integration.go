package integration

import (
	"context"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	coderHTTPPort       = "3000/tcp"
	coderStartupTimeout = 90 * time.Second
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
		image:   "ghcr.io/coder/coder",
		version: "latest",
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
	t.Logf("[%s] using coder image %s", name, ref)

	// Give Coder an external PostgreSQL on a per-test network instead of its
	// embedded one, which downloads a binary from Maven at startup (a flaky,
	// rate-limited fetch that reds CI lanes).
	nw, err := tcnetwork.New(ctx)
	testcontainers.CleanupNetwork(t, nw)
	require.NoError(t, err, "create test network")
	startPostgres(ctx, t, nw.Name)

	env := map[string]string{
		"CODER_HTTP_ADDRESS":      "0.0.0.0:3000",          // Listen on all interfaces inside the container.
		"CODER_ACCESS_URL":        "http://localhost:3000", // Avoid creating try.coder.app URLs.
		"CODER_TELEMETRY_ENABLE":  "false",                 // Avoid creating noise.
		"CODER_PG_CONNECTION_URL": "postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable",
	}
	if !opts.enableRateLimits {
		env["CODER_DANGEROUS_DISABLE_RATE_LIMITS"] = "true"
	}
	if opts.experiments != "" {
		env["CODER_EXPERIMENTS"] = opts.experiments
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:           ref,
			AlwaysPullImage: true,
			Env:             env,
			ExposedPorts:    []string{coderHTTPPort},
			Networks:        []string{nw.Name},
			WaitingFor:      wait.ForHTTP("/api/v2/buildinfo").WithPort(coderHTTPPort).WithStartupTimeout(coderStartupTimeout),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, ctr)
	t.Cleanup(func() {
		if t.Failed() && ctr != nil {
			dumpContainerLogs(t, ctr)
		}
	})
	require.NoError(t, err, "start coder container")

	endpoint, err := ctr.PortEndpoint(ctx, coderHTTPPort, "http")
	require.NoError(t, err, "coder endpoint")
	coderURL, err := url.Parse(endpoint)
	require.NoError(t, err, "parse coder URL")
	client := codersdk.New(coderURL)

	// nolint:gosec // For testing only.
	var (
		testEmail    = "admin@coder.com"
		testPassword = "InsecurePassw0rd!"
		testUsername = "admin"
	)

	// Perform first time setup. The wait strategy already blocked on
	// /api/v2/buildinfo returning 200, so the server is ready.
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

// startPostgres runs a throwaway PostgreSQL that Coder connects to instead of
// starting its embedded one. It's reachable at hostname "postgres" on the given
// network. Coder retries its database connection on startup, so we don't wait
// for it.
func startPostgres(ctx context.Context, t *testing.T, networkName string) {
	t.Helper()
	const ref = "us-docker.pkg.dev/coder-v2-images-public/public/postgres:17"
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:           ref,
			AlwaysPullImage: true,
			Env:             map[string]string{"POSTGRES_PASSWORD": "postgres"},
			Networks:        []string{networkName},
			NetworkAliases:  map[string][]string{networkName: {"postgres"}},
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err, "start postgres container")
}

func dumpContainerLogs(t *testing.T, ctr testcontainers.Container) {
	t.Helper()
	if ctr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logs, err := ctr.Logs(ctx)
	if err != nil {
		t.Logf("failed to fetch coder logs: %v", err)
		return
	}
	defer func() {
		if err := logs.Close(); err != nil {
			t.Logf("error closing container log reader: %v", err)
		}
	}()
	// testcontainers already demultiplexes the stream for non-TTY containers,
	// so read the plain log output directly.
	out, err := io.ReadAll(logs)
	if err != nil {
		t.Logf("failed to read coder logs: %v", err)
		return
	}
	t.Logf("=== coder container logs ===\n%s=== end coder container logs ===", out)
}
