package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

const EncryptionChunkSize = 12

type StreamDecryptor struct {
	aead      cipher.AEAD
	rc        io.ReadCloser
	ri        int // read index for plaintext
	err       error
	nonce     []byte
	plaintext []byte
}

func NewDecryptor(rc io.ReadCloser, key [32]byte) (*StreamDecryptor, error) {
	// Initialize a block cipher
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	// Choose a block cipher mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Read the nonce from beginning of the stream
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rc, nonce)
	if err != nil {
		return nil, fmt.Errorf("cannot read nonce: %s", err)
	}

	// fmt.Fprintf(os.Stderr, "decryption nonce: %q\n", nonce)

	return &StreamDecryptor{
		aead:  gcm,
		nonce: nonce,
		rc:    rc,
	}, nil
}

func (sd *StreamDecryptor) Close() error {
	sd.nonce = nil
	sd.plaintext = nil
	err := sd.rc.Close()
	if sd.err == nil {
		sd.err = err
	}
	return sd.err
}

//                ri
//                |
//                v
// abcdefghijklmnopqrstuvwxyz
// |<- already ->|

func (sd *StreamDecryptor) Read(buf []byte) (int, error) {
	if sd.err != nil {
		return 0, sd.err
	}

	var bi int

	// When more room in client's buf
	for len(buf) > bi {
		// When nothing left to copy from plaintext buffer
		if sd.ri == len(sd.plaintext) {
			// Read the number of ciphertext bytes that are available.
			var sbuf [8]byte
			_, sd.err = io.ReadFull(sd.rc, sbuf[:])
			if sd.err != nil {
				return bi, sd.err
			}

			size := int(binary.BigEndian.Uint64(sbuf[:]))
			ciphertext := make([]byte, size)

			// Read the ciphertext bytes.
			_, sd.err = io.ReadFull(sd.rc, ciphertext)
			if sd.err != nil {
				return bi, sd.err
			}

			// Then decrypt into the plaintext buffer.
			sd.plaintext, sd.err = sd.aead.Open(nil, sd.nonce, ciphertext, nil)
			if sd.err != nil {
				return bi, sd.err
			}
			sd.ri = 0
		}

		// Copy data from plaintext buffer to client buffer
		nc := copy(buf[bi:], sd.plaintext[sd.ri:])
		bi += nc
		sd.ri += nc
	}

	return bi, nil
}

type StreamEncryptor struct {
	aead      cipher.AEAD
	wc        io.WriteCloser
	pi        int
	err       error
	nonce     []byte
	plaintext []byte
}

func NewEncryptor(wc io.WriteCloser, key [32]byte) (*StreamEncryptor, error) {
	// Initialize a block cipher
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	// Choose a block cipher mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Generate a randomized nonce
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return nil, err
	}

	// Write the nonce to beginning of the stream
	_, err = wc.Write(nonce)
	if err != nil {
		return nil, fmt.Errorf("cannot write nonce: %s", err)
	}

	// fmt.Fprintf(os.Stderr, "encryption nonce: %q\n", nonce)

	return &StreamEncryptor{
		aead:      gcm,
		nonce:     nonce,
		wc:        wc,
		plaintext: make([]byte, EncryptionChunkSize),
	}, nil
}

func (se *StreamEncryptor) Close() error {
	err := se.Flush()
	se.plaintext = nil // future Write operations will panic
	if err2 := se.wc.Close(); err == nil {
		err = err2
	}
	// Only overwrite instance error when it is already nil.
	if se.err == nil {
		se.err = err
	}
	return se.err
}

func (se *StreamEncryptor) Flush() error {
	if se.err != nil {
		return se.err
	}
	if se.pi > 0 {
		_, se.err = se.writeFrame(se.plaintext[:se.pi])
		se.pi = 0
	}
	return se.err
}

func (se *StreamEncryptor) Write(buf []byte) (int, error) {
	if se.err != nil {
		return 0, se.err
	}

	// When new data will fit into plaintext buffer, append it.
	if len(se.plaintext) >= se.pi+len(buf) {
		nc := copy(se.plaintext[se.pi:], buf)
		se.pi += nc
		return nc, nil
	}

	// New data will not fit onto plaintext buffer, so send existing plaintext
	// buffer.
	if se.err = se.Flush(); se.err != nil {
		return 0, se.err
	}

	// When new data will fit into plaintext buffer, append it.
	if len(se.plaintext) >= len(buf) {
		se.pi = copy(se.plaintext, buf)
		return se.pi, nil
	}

	// Send this blob
	_, se.err = se.writeFrame(buf)
	if se.err != nil {
		return 0, se.err
	}
	return len(buf), nil
}

func (se *StreamEncryptor) writeFrame(buf []byte) (int, error) {
	// fmt.Fprintf(os.Stderr, "writeFrame(%q)\n", buf)
	if se.err != nil {
		return 0, se.err
	}

	ciphertext := se.aead.Seal(nil, se.nonce, buf, nil)

	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(ciphertext)))
	nw, err := se.wc.Write(size[:])
	if err != nil {
		return nw, fmt.Errorf("cannot write length: %s", err)
	}

	nw2, err := se.wc.Write(ciphertext)
	return nw + nw2, err
}
