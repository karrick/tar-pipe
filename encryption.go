package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
)

func Key32FromPassphrase(tag, passphrase string) [32]byte {
	var key [32]byte

	h := hmac.New(sha512.New512_256, []byte(tag))
	_, _ = h.Write([]byte(passphrase)) // hmac Write never returns error

	// Copy first 32 bytes of the HMAC digest into the key
	copy(key[:], h.Sum(nil)[:32])

	return key
}

const EncryptionChunkSize = 1024

type StreamDecryptor struct {
	aead      cipher.AEAD
	ior       io.Reader
	idx       int // read index for plaintext
	nonceSize int
	err       error
	plaintext []byte
}

func NewDecryptor(rc io.Reader, key [32]byte) (*StreamDecryptor, error) {
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

	return &StreamDecryptor{
		aead:      gcm,
		ior:       rc,
		nonceSize: gcm.NonceSize(),
	}, nil
}

func (sd *StreamDecryptor) Close() error {
	sd.aead = nil
	sd.ior = nil
	sd.plaintext = nil
	return sd.err
}

//                idx
//                |
//                v
// abcdefghijklmnopqrstuvwxyz
// |<- already ->|

func (sd *StreamDecryptor) Read(buf []byte) (int, error) {
	if sd.err != nil {
		return 0, sd.err
	}

	var idx int

	// When more room in client's buf
	for len(buf) > idx {
		// When nothing left to copy from plaintext buffer
		if sd.idx == len(sd.plaintext) {
			// Read the number of ciphertext bytes that are available.
			var sizeBuffer [8]byte
			_, sd.err = io.ReadFull(sd.ior, sizeBuffer[:])
			if sd.err != nil {
				return idx, sd.err
			}

			size := int(binary.BigEndian.Uint64(sizeBuffer[:]))
			ciphertext := make([]byte, size)

			// Read the ciphertext bytes.
			_, sd.err = io.ReadFull(sd.ior, ciphertext)
			if sd.err != nil {
				return idx, fmt.Errorf("cannot read %d byte frame: %s", size, sd.err)
			}

			// Then decrypt into the plaintext buffer.
			sd.plaintext, sd.err = sd.aead.Open(sd.plaintext[:0], ciphertext[:sd.nonceSize], ciphertext[sd.nonceSize:], nil)
			if sd.err != nil {
				return idx, fmt.Errorf("cannot decrypt ciphertext: %s", sd.err)
			}
			sd.idx = 0
		}

		// Copy data from plaintext buffer to client buffer
		nc := copy(buf[idx:], sd.plaintext[sd.idx:])
		idx += nc
		sd.idx += nc
	}

	return idx, nil
}

type StreamEncryptor struct {
	aead      cipher.AEAD
	iow       io.Writer
	idx       int
	nonceSize int
	err       error
	plaintext []byte
}

func NewEncryptor(wc io.Writer, key [32]byte) (*StreamEncryptor, error) {
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

	return &StreamEncryptor{
		aead:      gcm,
		iow:       wc,
		plaintext: make([]byte, EncryptionChunkSize),
		nonceSize: gcm.NonceSize(),
	}, nil
}

func (se *StreamEncryptor) Close() error {
	err := se.Flush()
	// Only overwrite instance error when it is already nil.
	if se.err == nil {
		se.err = err
	}
	se.aead = nil
	se.iow = nil
	return se.err
}

func (se *StreamEncryptor) Flush() error {
	if se.err != nil {
		return se.err
	}
	if se.idx > 0 {
		_, se.err = se.writeFrame(se.plaintext[:se.idx])
		se.idx = 0
	}
	return se.err
}

func (se *StreamEncryptor) Write(buf []byte) (int, error) {
	if se.err != nil {
		return 0, se.err
	}

	// When new data will fit into plaintext buffer, append it.
	if len(se.plaintext) >= se.idx+len(buf) {
		nc := copy(se.plaintext[se.idx:], buf)
		se.idx += nc
		return nc, nil
	}

	// New data will not fit onto plaintext buffer, so send existing plaintext
	// buffer.
	if se.err = se.Flush(); se.err != nil {
		return 0, se.err
	}

	// When new data will fit into plaintext buffer, append it.
	if len(se.plaintext) >= len(buf) {
		se.idx = copy(se.plaintext, buf)
		return se.idx, nil
	}

	// Send this blob
	_, se.err = se.writeFrame(buf)
	if se.err != nil {
		return 0, se.err
	}
	return len(buf), nil
}

func (se *StreamEncryptor) writeFrame(buf []byte) (int, error) {
	if se.err != nil {
		return 0, se.err
	}

	// Generate a random nonce for this frame
	nonce := make([]byte, se.nonceSize)
	_, se.err = io.ReadFull(rand.Reader, nonce)
	if se.err != nil {
		return 0, se.err
	}

	ciphertext := se.aead.Seal(nonce, nonce, buf, nil)

	var sizeBuffer [8]byte
	binary.BigEndian.PutUint64(sizeBuffer[:], uint64(len(ciphertext)))
	nw1, err := se.iow.Write(sizeBuffer[:])
	if err != nil {
		return nw1, fmt.Errorf("cannot write frame length: %s", err)
	}

	nw2, err := se.iow.Write(ciphertext)
	return nw1 + nw2, err
}
