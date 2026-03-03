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
	logs, done, err := waitForJobOnce(context.Background(), client, version, 0)
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
	_, done, err := waitForJobOnce(context.Background(), client, version, 0)
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
	_, done, err := waitForJobOnce(context.Background(), client, version, 0)
	require.NoError(t, err)
	require.False(t, done)
}

func TestWaitForJob_UsesAfterCursorAcrossRetries(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	var versionCallCount atomic.Int32
	var wsCallCount atomic.Int32
	secondAfterCh := make(chan string, 1)

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/templateversions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "follow") {
			call := wsCallCount.Add(1)
			if call == 2 {
				secondAfterCh <- r.URL.Query().Get("after")
			}

			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ctx := r.Context()
			if call == 1 {
				_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{ID: 1, Output: "log 1"})
				_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{ID: 2, Output: "log 2"})
				_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{ID: 3, Output: "log 3"})
			} else {
				_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{ID: 4, Output: "log 4"})
				_ = wsjson.Write(ctx, conn, codersdk.ProvisionerJobLog{ID: 5, Output: "log 5"})
			}
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
			ID:  versionID,
			Job: codersdk.ProvisionerJob{Status: status},
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
	require.Len(t, logs, 5)
	for i, log := range logs {
		require.Equal(t, int64(i+1), log.ID)
	}
	require.Equal(t, int32(2), wsCallCount.Load())
	select {
	case got := <-secondAfterCh:
		require.Equal(t, "3", got)
	default:
		t.Fatal("missing second after cursor")
	}
}

func TestWaitForJob_ContextCanceledDuringBackoff(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var statusCallCount atomic.Int32
	firstStatusSeen := make(chan struct{}, 1)
	go func() {
		<-firstStatusSeen
		cancel()
	}()

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
			ID:  versionID,
			Job: codersdk.ProvisionerJob{Status: codersdk.ProvisionerJobRunning},
		})
		// Cancel after the first status response so waitForJob hits cancellation while waiting to retry.
		if statusCallCount.Add(1) == 1 {
			firstStatusSeen <- struct{}{}
		}
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(srvURL)

	version := &codersdk.TemplateVersion{ID: versionID}
	_, err = waitForJob(ctx, client, version)
	require.ErrorIs(t, err, context.Canceled)
}
