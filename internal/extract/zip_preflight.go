package extract

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

func openBoundedZIP(ctx context.Context, path string) (*zip.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := preflightZIP(ctx, path); err != nil {
		return nil, fmt.Errorf("preflight zip: %w", err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	if len(archive.File) <= maxZipEntries {
		return archive, nil
	}
	return nil, errors.Join(ErrArchiveTooLarge, archive.Close())
}

const (
	zipEndSize        = 22
	zipMaxCommentSize = 1<<16 - 1
	zip64LocatorSize  = 20
	zip64EndMinSize   = 56
	zipEndSignature   = 0x06054b50
	zip64EndSignature = 0x06064b50
	zip64LocSignature = 0x07064b50
)

type zipPreflight struct {
	ctx  context.Context
	file *os.File
}

type zipDirectoryRegion struct {
	offset          int64
	size            int64
	expectedEntries uint64
}

func preflightZIP(ctx context.Context, path string) (err error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < zipEndSize {
		return zip.ErrFormat
	}
	var tail [zipEndSize + zipMaxCommentSize]byte
	tailSize := min(info.Size(), int64(len(tail)))
	buffer := tail[:tailSize]
	if _, err = file.ReadAt(buffer, info.Size()-tailSize); err != nil {
		return err
	}
	endOffset, err := findZIPEnd(ctx, buffer)
	if err != nil {
		return err
	}
	end := buffer[endOffset:]
	absoluteEndOffset := info.Size() - tailSize + int64(endOffset)
	preflight := zipPreflight{ctx: ctx, file: file}
	return preflight.inspectZIPEnd(end, absoluteEndOffset)
}

func findZIPEnd(ctx context.Context, tail []byte) (int, error) {
	for offset := len(tail) - zipEndSize; offset >= 0; offset-- {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if binary.LittleEndian.Uint32(tail[offset:]) != zipEndSignature {
			continue
		}
		commentSize := int(binary.LittleEndian.Uint16(tail[offset+20:]))
		if offset+zipEndSize+commentSize <= len(tail) {
			return offset, nil
		}
	}
	return 0, zip.ErrFormat
}

func (p *zipPreflight) inspectZIPEnd(end []byte, endOffset int64) error {
	disk := binary.LittleEndian.Uint16(end[4:])
	directoryDisk := binary.LittleEndian.Uint16(end[6:])
	diskEntries := binary.LittleEndian.Uint16(end[8:])
	totalEntries := binary.LittleEndian.Uint16(end[10:])
	directorySize := binary.LittleEndian.Uint32(end[12:])
	directoryOffset := binary.LittleEndian.Uint32(end[16:])
	usesZIP64 := disk == math.MaxUint16 || directoryDisk == math.MaxUint16 ||
		diskEntries == math.MaxUint16 || totalEntries == math.MaxUint16 ||
		directorySize == math.MaxUint32 || directoryOffset == math.MaxUint32
	if usesZIP64 {
		return p.inspectZIP64End(endOffset)
	}
	if disk != 0 || directoryDisk != 0 || diskEntries != totalEntries {
		return zip.ErrFormat
	}
	if uint64(directorySize) > maxZipDirectoryBytes {
		return ErrArchiveTooLarge
	}
	if totalEntries > maxZipEntries {
		return ErrArchiveTooLarge
	}
	directorySizeValue := int64(directorySize)
	directoryOffsetValue := int64(directoryOffset)
	if directorySizeValue > endOffset {
		return zip.ErrFormat
	}
	actualDirectoryOffset := endOffset - directorySizeValue
	if directoryOffsetValue > actualDirectoryOffset {
		return zip.ErrFormat
	}
	return scanZIPDirectory(p, zipDirectoryRegion{
		offset: actualDirectoryOffset, size: directorySizeValue, expectedEntries: uint64(totalEntries),
	})
}

func (p *zipPreflight) inspectZIP64End(endOffset int64) error {
	recordOffset, locatorOffset, err := p.readZIP64Locator(endOffset)
	if err != nil {
		return err
	}
	record, err := p.readZIP64End(recordOffset, locatorOffset)
	if err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(record[16:]) != 0 || binary.LittleEndian.Uint32(record[20:]) != 0 {
		return zip.ErrFormat
	}
	diskEntries := binary.LittleEndian.Uint64(record[24:])
	totalEntries := binary.LittleEndian.Uint64(record[32:])
	if diskEntries != totalEntries {
		return zip.ErrFormat
	}
	if totalEntries > maxZipEntries {
		return ErrArchiveTooLarge
	}
	directorySize := binary.LittleEndian.Uint64(record[40:])
	directoryOffset := binary.LittleEndian.Uint64(record[48:])
	if directorySize > maxZipDirectoryBytes {
		return ErrArchiveTooLarge
	}
	if directoryOffset > math.MaxInt64 || directorySize > math.MaxInt64 {
		return zip.ErrFormat
	}
	directoryOffsetValue := int64(directoryOffset)
	directorySizeValue := int64(directorySize)
	if directorySizeValue > recordOffset || directoryOffsetValue > recordOffset-directorySizeValue {
		return zip.ErrFormat
	}
	actualDirectoryOffset := recordOffset - directorySizeValue
	return scanZIPDirectory(p, zipDirectoryRegion{
		offset: actualDirectoryOffset, size: directorySizeValue, expectedEntries: totalEntries,
	})
}

func (p *zipPreflight) readZIP64Locator(endOffset int64) (int64, int64, error) {
	locatorOffset := endOffset - zip64LocatorSize
	if locatorOffset < 0 {
		return 0, 0, zip.ErrFormat
	}
	var locator [zip64LocatorSize]byte
	if err := readZIPAt(p.file, locator[:], locatorOffset); err != nil {
		return 0, 0, err
	}
	if binary.LittleEndian.Uint32(locator[:]) != zip64LocSignature ||
		binary.LittleEndian.Uint32(locator[4:]) != 0 ||
		binary.LittleEndian.Uint32(locator[16:]) != 1 {
		return 0, 0, zip.ErrFormat
	}
	recordOffset := binary.LittleEndian.Uint64(locator[8:])
	if recordOffset > math.MaxInt64 {
		return 0, 0, zip.ErrFormat
	}
	recordOffsetValue := int64(recordOffset)
	if recordOffsetValue > locatorOffset {
		return 0, 0, zip.ErrFormat
	}
	return recordOffsetValue, locatorOffset, nil
}

func (p *zipPreflight) readZIP64End(recordOffset, locatorOffset int64) ([zip64EndMinSize]byte, error) {
	var record [zip64EndMinSize]byte
	if err := readZIPAt(p.file, record[:], recordOffset); err != nil {
		return record, err
	}
	if binary.LittleEndian.Uint32(record[:]) != zip64EndSignature {
		return record, zip.ErrFormat
	}
	recordSize := binary.LittleEndian.Uint64(record[4:])
	if recordSize < 44 || recordSize > math.MaxInt64-12 {
		return record, zip.ErrFormat
	}
	if int64(recordSize)+12 != locatorOffset-recordOffset {
		return record, zip.ErrFormat
	}
	return record, nil
}

func readZIPAt(file *os.File, buffer []byte, offset int64) error {
	read, err := file.ReadAt(buffer, offset)
	if read != len(buffer) {
		return zip.ErrFormat
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
