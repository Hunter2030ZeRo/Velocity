package extract

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"math"
)

const (
	maxZipDirectoryBytes        uint64 = 64 << 20
	zipDirectoryHeaderSize             = 46
	zipDirectoryHeaderSignature        = 0x02014b50
	zipDirectorySignature              = 0x05054b50
	zipDirectorySignatureSize          = 6
	zipDirectoryScanBufferSize         = 32 << 10
)

type zipDirectoryScanner struct {
	ctx       context.Context
	reader    *bufio.Reader
	entries   uint64
	remaining int64
	discard   [zipDirectoryScanBufferSize]byte
	header    [zipDirectoryHeaderSize - 4]byte
	signature [4]byte
	size      [2]byte
}

func scanZIPDirectory(preflight *zipPreflight, region zipDirectoryRegion) error {
	if region.offset < 0 || region.size < 0 || region.offset > math.MaxInt64-region.size {
		return zip.ErrFormat
	}
	scanner := zipDirectoryScanner{
		ctx: preflight.ctx,
		reader: bufio.NewReaderSize(
			io.NewSectionReader(preflight.file, region.offset, region.size),
			zipDirectoryScanBufferSize,
		),
		remaining: region.size,
	}
	if err := scanner.scan(); err != nil {
		return err
	}
	if scanner.entries != region.expectedEntries {
		return zip.ErrFormat
	}
	return nil
}

func (s *zipDirectoryScanner) scan() error {
	for s.remaining > 0 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if err := s.read(s.signature[:]); err != nil {
			return err
		}
		switch binary.LittleEndian.Uint32(s.signature[:]) {
		case zipDirectoryHeaderSignature:
			if err := s.scanFileHeader(); err != nil {
				return err
			}
		case zipDirectorySignature:
			return s.scanDirectorySignature()
		default:
			return zip.ErrFormat
		}
	}
	return nil
}

func (s *zipDirectoryScanner) scanFileHeader() error {
	if err := s.read(s.header[:]); err != nil {
		return err
	}
	if binary.LittleEndian.Uint16(s.header[30:]) != 0 {
		return zip.ErrFormat
	}
	nameSize := int64(binary.LittleEndian.Uint16(s.header[24:]))
	extraSize := int64(binary.LittleEndian.Uint16(s.header[26:]))
	commentSize := int64(binary.LittleEndian.Uint16(s.header[28:]))
	if err := s.skip(nameSize + extraSize + commentSize); err != nil {
		return err
	}
	s.entries++
	if s.entries > maxZipEntries {
		return ErrArchiveTooLarge
	}
	return nil
}

func (s *zipDirectoryScanner) scanDirectorySignature() error {
	if err := s.read(s.size[:]); err != nil {
		return err
	}
	if int64(binary.LittleEndian.Uint16(s.size[:])) != s.remaining {
		return zip.ErrFormat
	}
	return s.skip(s.remaining)
}

func (s *zipDirectoryScanner) read(buffer []byte) error {
	if int64(len(buffer)) > s.remaining {
		return zip.ErrFormat
	}
	remaining := buffer
	for len(remaining) > 0 {
		read, err := s.reader.Read(remaining)
		if read > 0 {
			remaining = remaining[read:]
		}
		if err != nil || read == 0 {
			return zip.ErrFormat
		}
	}
	s.remaining -= int64(len(buffer))
	return nil
}

func (s *zipDirectoryScanner) skip(size int64) error {
	if size < 0 || size > s.remaining {
		return zip.ErrFormat
	}
	for size > 0 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		chunk := min(size, int64(len(s.discard)))
		if err := s.read(s.discard[:chunk]); err != nil {
			return err
		}
		size -= chunk
	}
	return nil
}
