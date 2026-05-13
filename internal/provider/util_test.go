package provider

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/stretchr/testify/require"
)

func TestComputeArchiveHash(t *testing.T) {
	t.Parallel()

	t.Run("ValidTarFile", func(t *testing.T) {
		t.Parallel()
		// Create a minimal tar file
		tarPath := filepath.Join(t.TempDir(), "test.tar")
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		content := []byte("hello world")
		err := tw.WriteHeader(&tar.Header{
			Name: "test.txt",
			Size: int64(len(content)),
			Mode: 0644,
		})
		require.NoError(t, err)
		_, err = tw.Write(content)
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		err = os.WriteFile(tarPath, buf.Bytes(), 0644)
		require.NoError(t, err)

		hash, err := computeArchiveHash(tarPath)
		require.NoError(t, err)
		require.NotEmpty(t, hash)
		require.Len(t, hash, 64) // SHA-256 hex = 64 chars

		// Same file should produce same hash
		hash2, err := computeArchiveHash(tarPath)
		require.NoError(t, err)
		require.Equal(t, hash, hash2)
	})

	t.Run("ValidZipFile", func(t *testing.T) {
		t.Parallel()
		zipPath := filepath.Join(t.TempDir(), "test.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		fw, err := zw.Create("test.txt")
		require.NoError(t, err)
		_, err = fw.Write([]byte("hello world"))
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		err = os.WriteFile(zipPath, buf.Bytes(), 0644)
		require.NoError(t, err)

		hash, err := computeArchiveHash(zipPath)
		require.NoError(t, err)
		require.NotEmpty(t, hash)
		require.Len(t, hash, 64)
	})

	t.Run("DifferentContentDifferentHash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		file1 := filepath.Join(dir, "a.tar")
		err := os.WriteFile(file1, []byte("content-a"), 0644)
		require.NoError(t, err)

		file2 := filepath.Join(dir, "b.tar")
		err = os.WriteFile(file2, []byte("content-b"), 0644)
		require.NoError(t, err)

		hash1, err := computeArchiveHash(file1)
		require.NoError(t, err)
		hash2, err := computeArchiveHash(file2)
		require.NoError(t, err)
		require.NotEqual(t, hash1, hash2)
	})

	t.Run("NonexistentFile", func(t *testing.T) {
		t.Parallel()
		_, err := computeArchiveHash("/nonexistent/path.tar")
		require.Error(t, err)
	})
}

func TestArchiveContentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		expected    string
		expectError bool
	}{
		{
			name:     "TarFile",
			path:     "/path/to/template.tar",
			expected: codersdk.ContentTypeTar,
		},
		{
			name:     "ZipFile",
			path:     "/path/to/template.zip",
			expected: codersdk.ContentTypeZip,
		},
		{
			name:        "TarGzFile",
			path:        "/path/to/template.tar.gz",
			expectError: true,
		},
		{
			name:        "RandomFile",
			path:        "/path/to/template.txt",
			expectError: true,
		},
		{
			name:        "NoExtension",
			path:        "/path/to/template",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ct, err := archiveContentType(tc.path)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, ct)
			}
		})
	}
}

func TestValidateArchiveSize(t *testing.T) {
	t.Parallel()

	t.Run("ValidArchiveUnder100MiB", func(t *testing.T) {
		t.Parallel()
		tmpFile, err := os.CreateTemp(t.TempDir(), "archive_*.tar")
		require.NoError(t, err)
		defer tmpFile.Close() //nolint:errcheck

		// Create a 10 MiB file
		_, err = tmpFile.Write(make([]byte, 10*1024*1024))
		require.NoError(t, err)

		err = validateArchiveSize(tmpFile.Name())
		require.NoError(t, err)
	})

	t.Run("InvalidArchiveExceeds100MiB", func(t *testing.T) {
		t.Parallel()
		tmpFile, err := os.CreateTemp(t.TempDir(), "archive_*.tar")
		require.NoError(t, err)
		defer tmpFile.Close() //nolint:errcheck

		// Create a 101 MiB file (exceeds limit)
		_, err = tmpFile.Write(make([]byte, 101*1024*1024))
		require.NoError(t, err)

		err = validateArchiveSize(tmpFile.Name())
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds 100 MiB limit")
	})

	t.Run("NonexistentFile", func(t *testing.T) {
		t.Parallel()
		err := validateArchiveSize("/nonexistent/path/archive.tar")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to stat archive")
	})

	t.Run("ExactlyAtLimit", func(t *testing.T) {
		t.Parallel()
		tmpFile, err := os.CreateTemp(t.TempDir(), "archive_*.tar")
		require.NoError(t, err)
		defer tmpFile.Close() //nolint:errcheck

		// Create exactly 100 MiB file
		_, err = tmpFile.Write(make([]byte, 100*1024*1024))
		require.NoError(t, err)

		err = validateArchiveSize(tmpFile.Name())
		require.NoError(t, err)
	})
}
