package pen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

var EBADSLT = errors.New("checksum mismatch")

// each ReadFromReader requires 2 syscalls, one to read the header and one to read the data (since the length of the data is in the header)
// you can reduce that to 1 syscall if your data fits within 1 block, do not set BLOCK_SIZE < 16 because this is the header length
var BLOCK_SIZE = 4096

type Reader struct {
	file *os.File
}

// Create New AppendReader (you just nice wrapper around ReadFromReader adn ScanFromReader)
// it is *safe* to use it concurrently
// Example usage
//	r, err := NewReader(filename)
//	if err != nil {
//		panic(err)
//	}
//	// read specific offset
//	data, _, err := r.Read(docID)
//	if err != nil {
//		panic(err)
//	}
//	// scan from specific offset
//	err = r.Scan(0, func(data []byte, offset, next uint32) error {
//		log.Printf("%v",data)
//		return nil
//	})
//
func NewReader(filename string) (*Reader, error) {
	fd, err := os.OpenFile(filename, os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}

	return &Reader{
		file: fd,
	}, nil
}

// Scan the open file, if the callback returns error this error is returned as the Scan error. just a wrapper around ScanFromReader.
func (ar *Reader) Scan(offset uint32, cb func([]byte, uint32, uint32) error) error {
	return ScanFromReader(ar.file, offset, cb)
}

// Read at specific offset (just wrapper around ReadFromReader), returns the data, next readable offset and error
func (ar *Reader) Read(offset uint32) ([]byte, uint32, error) {
	return ReadFromReader(ar.file, offset)
}

func (ar *Reader) Close() error {
	return ar.file.Close()
}

// Reads specific offset. returns data, nextOffset, error. You can
// ReadFromReader(nextOffset) if you want to read the next document, or
// use the Scan() helper
func ReadFromReader(reader io.ReaderAt, offset uint32) ([]byte, uint32, error) {
	block := make([]byte, BLOCK_SIZE)
	// NB: BLOCK_SIZE should not be below 16
	n, err := reader.ReadAt(block, int64(offset*PAD))

	// end of file, or not enough space to read whole block_size
	if n < 16 {
		return nil, 0, err
	}
	if n != BLOCK_SIZE {
		block = block[:n]
	}

	header := block[:16]
	if !bytes.Equal(header[8:12], MAGIC) {
		return nil, 0, EBADSLT
	}

	computedChecksumHeader := uint32(Hash(header[:12]))
	checksumHeader := binary.LittleEndian.Uint32(header[12:16])
	if checksumHeader != computedChecksumHeader {
		return nil, 0, EBADSLT
	}

	metadataLen := binary.LittleEndian.Uint32(header)
	nextOffset := (offset + ((uint32(len(header))+(uint32(metadataLen)))+PAD-1)/PAD)

	var readInto []byte
	if int(metadataLen) < len(block)-len(header) {
		readInto = block[len(header) : len(header)+int(metadataLen)]
	} else {
		readInto = make([]byte, metadataLen)
		_, err = reader.ReadAt(readInto, int64(offset*PAD)+int64(len(header)))
		if err != nil {
			return nil, 0, err
		}
	}

	checksumHeaderData := binary.LittleEndian.Uint32(header[4:])
	computedChecksumData := uint32(Hash(readInto))

	if checksumHeaderData != computedChecksumData {
		return nil, 0, EBADSLT
	}
	return readInto, nextOffset, nil
}

// Scan ReaderAt, if the callback returns error this error is returned as the Scan error
func ScanFromReader(reader io.ReaderAt, offset uint32, cb func([]byte, uint32, uint32) error) error {
	for {
		data, next, err := ReadFromReader(reader, offset)
		if err == io.EOF {
			return nil
		}
		if err == EBADSLT {
			// assume corrupted file, so just skip until we find next valid entry
			offset++
			continue
		}
		if err != nil {
			return err
		}
		err = cb(data, offset, next)
		if err != nil {
			return err
		}
		offset = next
	}
}
