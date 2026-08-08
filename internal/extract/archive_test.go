package extract

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

type testEntry struct {
	paxRecords map[string]string
	name       string
	body       string
	mode       int64
	typeflag   byte
}

func Test_isRegularTarType_accepts_legacy_zero_flag_and_rejects_links(t *testing.T) {
	tests := map[string]struct {
		typeFlag byte
		want     bool
	}{
		"regular":       {typeFlag: tar.TypeReg, want: true},
		"legacy zero":   {typeFlag: 0, want: true},
		"hard link":     {typeFlag: tar.TypeLink, want: false},
		"symbolic link": {typeFlag: tar.TypeSymlink, want: false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// When
			got := isRegularTarType(test.typeFlag)

			// Then
			require.Equal(t, test.want, got)
		})
	}
}

func writeRaw(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "downloaded-tool")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func writeZip(t *testing.T, entries []testEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.mode != 0 {
			header.SetMode(os.FileMode(entry.mode))
		}
		out, createErr := writer.CreateHeader(header)
		require.NoError(t, createErr)
		_, err = io.WriteString(out, entry.body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	return path
}

func writeTar(t *testing.T, format string, entries []testEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture."+format)
	file, err := os.Create(path)
	require.NoError(t, err)
	compressed, compressErr := tarCompressor(format, file)
	if compressErr != nil {
		t.Fatal(compressErr)
		return ""
	}
	if compressed == nil {
		t.Fatal("create test compressor: nil writer")
		return ""
	}
	writer := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: typeflag,
			PAXRecords: entry.paxRecords,
		}
		if typeflag != tar.TypeReg {
			header.Size = 0
			header.Linkname = "target"
		}
		require.NoError(t, writer.WriteHeader(header))
		if header.Size > 0 {
			_, err = io.WriteString(writer, entry.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, file.Close())
	return path
}

func tarCompressor(format string, file *os.File) (io.WriteCloser, error) {
	switch format {
	case "tar.gz":
		writer := gzip.NewWriter(file)
		if writer == nil {
			return nil, fmt.Errorf("create %s compressor: nil writer", format)
		}
		return writer, nil
	case "tar.zst":
		writer, err := zstd.NewWriter(file)
		if err != nil {
			return nil, fmt.Errorf("create %s compressor: %w", format, err)
		}
		if writer == nil {
			return nil, fmt.Errorf("create %s compressor: nil writer", format)
		}
		return writer, nil
	default:
		return nil, fmt.Errorf("unsupported test format %q", format)
	}
}
