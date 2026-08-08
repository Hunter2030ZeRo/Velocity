package extract

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_preflightZIP_uses_trailing_shadow_end_record(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "shadowed-end.zip")
	require.NoError(t, os.WriteFile(path, zipWithShadowedEndRecord(), 0o600))

	// When
	err := preflightZIP(context.Background(), path)

	// Then
	require.ErrorIs(t, err, ErrArchiveTooLarge)
}

func zipWithShadowedEndRecord() []byte {
	base := zipWithModuloEntryCount(65_537)
	directorySize := len(base) - zipEndSize
	const trailingSize = 4
	contents := make([]byte, directorySize+2*zipEndSize+trailingSize)
	copy(contents, base[:directorySize])
	shadowed := contents[directorySize:]
	binary.LittleEndian.PutUint32(shadowed, zipEndSignature)
	binary.LittleEndian.PutUint16(shadowed[20:], zipEndSize+trailingSize)
	selected := shadowed[zipEndSize:]
	copy(selected, base[directorySize:])
	binary.LittleEndian.PutUint32(selected[12:], uint32(directorySize+zipEndSize))
	return contents
}
