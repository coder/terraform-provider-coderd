package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWaitForJobOnce_Success(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/templateversions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "follow") {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ctx := r.Context()
			_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{
				ID:     1,
				Output: "test log line",
			})
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codersdk.TemplateVersion{
			ID: versionID,
			Job: codersdk.ProvisionerJob{
				Status: codersdk.ProvisionerJobSucceeded,
			},
		})
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(srvURL)

	version := &codersdk.TemplateVersion{ID: versionID}
	logs, done, err := waitForJobOnce(context.Background(), client, version)
	require.NoError(t, err)
	require.True(t, done)
	require.Len(t, logs, 1)
	require.Equal(t, "test log line", logs[0].Output)
}

func TestWaitForJobOnce_JobFailed(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/templateversions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "follow") {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codersdk.TemplateVersion{
			ID: versionID,
			Job: codersdk.ProvisionerJob{
				Status: codersdk.ProvisionerJobFailed,
				Error:  "something went wrong",
			},
		})
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(srvURL)

	version := &codersdk.TemplateVersion{ID: versionID}
	_, done, err := waitForJobOnce(context.Background(), client, version)
	require.Error(t, err)
	require.False(t, done)
	require.Contains(t, err.Error(), "provisioner job did not succeed")
	require.Contains(t, err.Error(), "something went wrong")
}

func TestWaitForJobOnce_StillActive(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/templateversions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "follow") {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codersdk.TemplateVersion{
			ID: versionID,
			Job: codersdk.ProvisionerJob{
				Status: codersdk.ProvisionerJobRunning,
			},
		})
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(srvURL)

	version := &codersdk.TemplateVersion{ID: versionID}
	_, done, err := waitForJobOnce(context.Background(), client, version)
	require.NoError(t, err)
	require.False(t, done)
}

func TestWaitForJob_RetriesAndCloses(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	var wsConnections atomic.Int32

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/templateversions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "follow") {
			wsConnections.Add(1)
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codersdk.TemplateVersion{
			ID: versionID,
			Job: codersdk.ProvisionerJob{
				Status: codersdk.ProvisionerJobRunning,
			},
		})
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(srvURL)

	version := &codersdk.TemplateVersion{ID: versionID}
	_, err = waitForJob(context.Background(), client, version)
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not complete after 3 retries")
	require.Equal(t, int32(3), wsConnections.Load())
}



func TestWaitForJob_SucceedsOnRetry(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	var versionCallCount atomic.Int32

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/templateversions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "follow") {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ctx := r.Context()
			_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{
				ID:     int64(versionCallCount.Load()),
				Output: "log line",
			})
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			return
		}
		count := versionCallCount.Add(1)
		status := codersdk.ProvisionerJobRunning
		if count >= 2 {
			status = codersdk.ProvisionerJobSucceeded
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codersdk.TemplateVersion{
			ID: versionID,
			Job: codersdk.ProvisionerJob{
				Status: status,
			},
		})
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(srvURL)

	version := &codersdk.TemplateVersion{ID: versionID}
	logs, err := waitForJob(context.Background(), client, version)
	require.NoError(t, err)
	require.Len(t, logs, 2)
}
