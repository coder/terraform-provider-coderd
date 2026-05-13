package provider

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"io"
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

func TestNormalizeZip(t *testing.T) {
	t.Parallel()

	t.Run("AddsMissingDirectoryEntries", func(t *testing.T) {
		t.Parallel()
		// Create a zip with only file entries (no directory entries),
		// mimicking hashicorp/archive's archive_file data source.
		zipPath := filepath.Join(t.TempDir(), "nodirs.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		// Top-level regular file.
		fw, err := zw.Create("main.tf")
		require.NoError(t, err)
		_, err = fw.Write([]byte("resource {}"))
		require.NoError(t, err)

		// Top-level hidden file (dotfile at root).
		fw, err = zw.Create(".env")
		require.NoError(t, err)
		_, err = fw.Write([]byte("SECRET=value"))
		require.NoError(t, err)

		// Top-level hidden directory with a file inside.
		fw, err = zw.Create(".config/settings.json")
		require.NoError(t, err)
		_, err = fw.Write([]byte(`{"key": "value"}`))
		require.NoError(t, err)

		// Hidden file inside a regular directory.
		fw, err = zw.Create("subdir/.hidden")
		require.NoError(t, err)
		_, err = fw.Write([]byte("hidden content"))
		require.NoError(t, err)

		// Deeply nested hidden directory with a hidden file inside.
		fw, err = zw.Create("a/b/.secret-dir/.credentials")
		require.NoError(t, err)
		_, err = fw.Write([]byte("token=abc123"))
		require.NoError(t, err)

		require.NoError(t, zw.Close())
		err = os.WriteFile(zipPath, buf.Bytes(), 0644)
		require.NoError(t, err)

		// Verify original has no directory entries.
		origReader, err := zip.OpenReader(zipPath)
		require.NoError(t, err)
		for _, f := range origReader.File {
			require.False(t, f.FileInfo().IsDir(), "expected no dir entries in original, got %q", f.Name)
		}
		require.NoError(t, origReader.Close())

		// Normalize.
		data, err := normalizeZip(zipPath)
		require.NoError(t, err)

		// Read normalized zip and collect entries.
		normReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)

		dirs := make(map[string]bool)
		files := make(map[string]bool)
		for _, f := range normReader.File {
			if f.FileInfo().IsDir() {
				dirs[f.Name] = true
			} else {
				files[f.Name] = true
			}
		}

		// All intermediate directories should exist, including hidden ones.
		require.True(t, dirs[".config/"], "missing .config/")
		require.True(t, dirs["subdir/"], "missing subdir/")
		require.True(t, dirs["a/"], "missing a/")
		require.True(t, dirs["a/b/"], "missing a/b/")
		require.True(t, dirs["a/b/.secret-dir/"], "missing a/b/.secret-dir/")

		// All original files should still be present.
		require.True(t, files["main.tf"], "missing main.tf")
		require.True(t, files[".env"], "missing .env")
		require.True(t, files[".config/settings.json"], "missing .config/settings.json")
		require.True(t, files["subdir/.hidden"], "missing subdir/.hidden")
		require.True(t, files["a/b/.secret-dir/.credentials"], "missing a/b/.secret-dir/.credentials")
	})

	t.Run("PreservesDirectoryPermissions", func(t *testing.T) {
		t.Parallel()
		// Verify that synthesized directory entries have proper mode bits
		// so the server-side extractor can create them.
		zipPath := filepath.Join(t.TempDir(), "needsperms.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		fw, err := zw.Create("deep/nested/dir/file.txt")
		require.NoError(t, err)
		_, err = fw.Write([]byte("content"))
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		err = os.WriteFile(zipPath, buf.Bytes(), 0644)
		require.NoError(t, err)

		data, err := normalizeZip(zipPath)
		require.NoError(t, err)

		normReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)

		for _, f := range normReader.File {
			if f.FileInfo().IsDir() {
				mode := f.Mode()
				require.NotZero(t, mode&0700,
					"directory %q should have owner rwx bits set, got %s", f.Name, mode)
			}
		}
	})

	t.Run("NoChangeWhenDirsExist", func(t *testing.T) {
		t.Parallel()
		zipPath := filepath.Join(t.TempDir(), "withdirs.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		// Add directory entry explicitly.
		_, err := zw.Create("subdir/")
		require.NoError(t, err)
		fw, err := zw.Create("subdir/file.txt")
		require.NoError(t, err)
		_, err = fw.Write([]byte("content"))
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		origData := buf.Bytes()
		err = os.WriteFile(zipPath, origData, 0644)
		require.NoError(t, err)

		// Normalize should return original bytes unchanged.
		data, err := normalizeZip(zipPath)
		require.NoError(t, err)
		require.Equal(t, origData, data)
	})

	t.Run("PreservesFileContent", func(t *testing.T) {
		t.Parallel()
		// Verify normalization doesn't corrupt file contents,
		// including hidden files at various levels.
		zipPath := filepath.Join(t.TempDir(), "content.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		fileContents := map[string]string{
			".gitignore":                "node_modules/\n.env\n",
			"main.tf":                   "resource \"null_resource\" \"test\" {}",
			".vscode/settings.json":     `{"editor.formatOnSave": true}`,
			"scripts/.local/bin/deploy": "#!/bin/bash\necho deploy",
		}
		for name, content := range fileContents {
			fw, err := zw.Create(name)
			require.NoError(t, err)
			_, err = fw.Write([]byte(content))
			require.NoError(t, err)
		}
		require.NoError(t, zw.Close())
		err := os.WriteFile(zipPath, buf.Bytes(), 0644)
		require.NoError(t, err)

		data, err := normalizeZip(zipPath)
		require.NoError(t, err)

		normReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)

		for _, f := range normReader.File {
			if f.FileInfo().IsDir() {
				continue
			}
			expected, ok := fileContents[f.Name]
			require.True(t, ok, "unexpected file in normalized zip: %q", f.Name)
			rc, err := f.Open()
			require.NoError(t, err)
			actual, err := io.ReadAll(rc)
			require.NoError(t, rc.Close())
			require.NoError(t, err)
			require.Equal(t, expected, string(actual),
				"content mismatch for %q", f.Name)
		}
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
