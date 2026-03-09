package integration

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/stretchr/testify/require"
)

// createMinimalTar creates a small valid tar archive for uploading.
func createMinimalTar(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("# test file")
	err := tw.WriteHeader(&tar.Header{
		Name: "main.tf",
		Mode: 0o644,
		Size: int64(len(content)),
	})
	require.NoError(t, err)
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return &buf
}

// TestHeadersBypassRateLimit verifies that the X-Coder-Bypass-Ratelimit header
// allows an Owner to exceed the files endpoint rate limit (12 req/min).
//
// This test starts a Coder instance with rate limits ENABLED, then:
// 1. Confirms that rapid file GETs without the bypass header hit a 429.
// 2. Confirms that the same burst with the bypass header succeeds.
func TestHeadersBypassRateLimit(t *testing.T) {
	t.Parallel()
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
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(timeoutMins)*time.Minute)
	t.Cleanup(cancel)

	// Start Coder WITH rate limits enabled (no CODER_DANGEROUS_DISABLE_RATE_LIMITS).
	client := StartCoder(ctx, t, "headers-ratelimit", EnableRateLimits)

	// Upload a small file so we have something to GET.
	uploadResp, err := client.Upload(ctx, "application/x-tar", createMinimalTar(t))
	require.NoError(t, err, "upload file")
	fileID := uploadResp.ID

	// The files endpoint rate limit is 12 requests per minute.
	// Fire 15 rapid GETs without the bypass header -- we expect at least one 429.
	const burstCount = 15

	t.Run("WithoutBypass", func(t *testing.T) {
		t.Parallel()
		subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer subCancel()

		got429 := false
		for range burstCount {
			_, _, err := client.Download(subCtx, fileID)
			if err != nil {
				var sdkErr *codersdk.Error
				if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusTooManyRequests {
					got429 = true
					break
				}
			}
		}
		require.True(t, got429, "expected to hit 429 rate limit within %d requests", burstCount)
	})

	t.Run("WithBypass", func(t *testing.T) {
		t.Parallel()
		subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer subCancel()

		// Create a new client with the bypass header set.
		bypassClient := codersdk.New(client.URL)
		bypassClient.SetSessionToken(client.SessionToken())
		bypassClient.HTTPClient.Transport = &codersdk.HeaderTransport{
			Transport: http.DefaultTransport,
			Header: http.Header{
				"X-Coder-Bypass-Ratelimit": []string{"true"},
			},
		}

		// Same burst, but with bypass -- all should succeed.
		for i := range burstCount {
			_, _, err := bypassClient.Download(subCtx, fileID)
			require.NoError(t, err, "request %d should not be rate limited with bypass header", i+1)
		}
	})
}
