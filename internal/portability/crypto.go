package portability

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	saltLen      = 16
	nonceLen     = 12

	bundleFormatV2 = 0x02

	defaultChunkSize = 16 * 1024 * 1024 // 16 MiB
	maxChunkSize     = 128 * 1024 * 1024
)

const (
	minPassphraseLen  = 8
	weakPassphraseLen = 12
)

// SealWriter returns a WriteCloser that encrypts plaintext written to it using
// chunked AES-256-GCM and writes the sealed stream to dst. Each chunk carries
// its own nonce + GCM tag; tampering with any chunk is detected on Open.
// Caller MUST Close() to flush the final chunk.
func SealWriter(dst io.Writer, passphrase []byte) (io.WriteCloser, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	zeroize(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	hdr := make([]byte, 0, len(MagicHeader)+1+saltLen+4)
	hdr = append(hdr, MagicHeader...)
	hdr = append(hdr, bundleFormatV2)
	hdr = append(hdr, salt...)
	hdr = binary.BigEndian.AppendUint32(hdr, uint32(defaultChunkSize))
	if _, err := dst.Write(hdr); err != nil {
		return nil, fmt.Errorf("write bundle header: %w", err)
	}

	return &sealWriter{dst: dst, aead: aead, chunkSize: defaultChunkSize, buf: make([]byte, 0, defaultChunkSize)}, nil
}

type sealWriter struct {
	dst       io.Writer
	aead      cipher.AEAD
	chunkSize int
	buf       []byte
	closed    bool
}

func (w *sealWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("portability: SealWriter already closed")
	}
	total := len(p)
	for len(p) > 0 {
		space := w.chunkSize - len(w.buf)
		if space > len(p) {
			space = len(p)
		}
		w.buf = append(w.buf, p[:space]...)
		p = p[space:]
		if len(w.buf) >= w.chunkSize {
			if err := w.flushChunk(); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func (w *sealWriter) flushChunk() error {
	if len(w.buf) == 0 {
		return nil
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	ct := w.aead.Seal(nil, nonce, w.buf, nil)

	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(ct)))
	if _, err := w.dst.Write(nonce); err != nil {
		return fmt.Errorf("write nonce: %w", err)
	}
	if _, err := w.dst.Write(lenBuf); err != nil {
		return fmt.Errorf("write chunk len: %w", err)
	}
	if _, err := w.dst.Write(ct); err != nil {
		return fmt.Errorf("write chunk: %w", err)
	}
	w.buf = w.buf[:0]
	return nil
}

func (w *sealWriter) Close() error {
	if w.closed {
		return nil
	}
	if err := w.flushChunk(); err != nil {
		return err
	}
	w.closed = true
	return nil
}

// OpenReader returns a Reader that decrypts a streaming sealed bundle from src.
// Returns ErrBadPassphrase on any per-chunk authentication failure (wrong key
// or tampered ciphertext — indistinguishable by design).
func OpenReader(src io.Reader, passphrase []byte) (io.Reader, error) {
	hdrLen := len(MagicHeader) + 1 + saltLen + 4
	hdr := make([]byte, hdrLen)
	if _, err := io.ReadFull(src, hdr); err != nil {
		return nil, fmt.Errorf("read bundle header: %w", err)
	}
	off := 0
	if string(hdr[off:off+len(MagicHeader)]) != MagicHeader {
		return nil, errors.New("portability: invalid bundle header")
	}
	off += len(MagicHeader)
	ver := hdr[off]
	off++
	if ver != bundleFormatV2 {
		return nil, fmt.Errorf("portability: unsupported bundle format v%d", ver)
	}
	salt := hdr[off : off+saltLen]
	off += saltLen
	chunkMax := int(binary.BigEndian.Uint32(hdr[off : off+4]))
	if chunkMax <= 0 || chunkMax > maxChunkSize {
		return nil, errors.New("portability: chunk size out of range")
	}

	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	zeroize(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &sealReader{src: src, aead: aead, chunkMax: chunkMax}, nil
}

type sealReader struct {
	src      io.Reader
	aead     cipher.AEAD
	chunkMax int
	buf      []byte
	off      int
	eof      bool
}

func (r *sealReader) Read(p []byte) (int, error) {
	for r.off >= len(r.buf) {
		if r.eof {
			return 0, io.EOF
		}
		if err := r.readChunk(); err != nil {
			if errors.Is(err, io.EOF) {
				r.eof = true
				return 0, io.EOF
			}
			return 0, err
		}
	}
	n := copy(p, r.buf[r.off:])
	r.off += n
	return n, nil
}

func (r *sealReader) readChunk() error {
	nonce := make([]byte, nonceLen)
	_, err := io.ReadFull(r.src, nonce)
	if err == io.EOF {
		return io.EOF
	}
	if err == io.ErrUnexpectedEOF {
		return fmt.Errorf("portability: truncated bundle (nonce)")
	}
	if err != nil {
		return fmt.Errorf("read nonce: %w", err)
	}

	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r.src, lenBuf); err != nil {
		return fmt.Errorf("portability: truncated bundle (chunk len): %w", err)
	}
	ctLen := int(binary.BigEndian.Uint32(lenBuf))
	if ctLen < 16 || ctLen > r.chunkMax+16 {
		return errors.New("portability: chunk length out of range")
	}
	ct := make([]byte, ctLen)
	if _, err := io.ReadFull(r.src, ct); err != nil {
		return fmt.Errorf("portability: truncated bundle (chunk): %w", err)
	}
	pt, err := r.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return ErrBadPassphrase
	}
	r.buf = pt
	r.off = 0
	return nil
}

// Seal is a convenience wrapper over SealWriter. For large plaintexts, prefer
// SealWriter to avoid double allocation.
func Seal(plaintext, passphrase []byte) ([]byte, error) {
	var buf bytes.Buffer
	sw, err := SealWriter(&buf, passphrase)
	if err != nil {
		return nil, err
	}
	if _, err := sw.Write(plaintext); err != nil {
		return nil, err
	}
	if err := sw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Open is a convenience wrapper over OpenReader. For large bundles, prefer
// OpenReader chained with tar/gzip readers to keep memory bounded.
func Open(sealed, passphrase []byte) ([]byte, error) {
	sr, err := OpenReader(bytes.NewReader(sealed), passphrase)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(sr)
}

func deriveKey(passphrase, salt []byte) ([]byte, error) {
	key, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive scrypt key: %w", err)
	}
	return key, nil
}

func ValidatePassphrase(pass []byte) (warning error, fatal error) {
	switch {
	case len(pass) < minPassphraseLen:
		return nil, fmt.Errorf("passphrase must be at least %d characters", minPassphraseLen)
	case len(pass) < weakPassphraseLen:
		return fmt.Errorf("weak passphrase (< %d chars)", weakPassphraseLen), nil
	default:
		return nil, nil
	}
}

func zeroize(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
