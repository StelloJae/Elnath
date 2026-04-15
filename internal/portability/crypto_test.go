package portability

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCryptoSealOpenRoundTrip(t *testing.T) {
	plaintext := []byte("portable data")
	sealed, err := Seal(plaintext, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	opened, err := Open(sealed, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
}

func TestCryptoWrongPassphrase(t *testing.T) {
	sealed, err := Seal([]byte("secret"), []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	_, err = Open(sealed, []byte("different-passphrase"))
	if !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("Open error = %v, want ErrBadPassphrase", err)
	}
}

func TestCryptoTamperedCiphertext(t *testing.T) {
	sealed, err := Seal([]byte("secret"), []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed[len(sealed)-1] ^= 0xFF

	_, err = Open(sealed, []byte("strong-passphrase"))
	if !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("Open error = %v, want ErrBadPassphrase", err)
	}
}

func TestCryptoEmptyPlaintext(t *testing.T) {
	sealed, err := Seal(nil, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	opened, err := Open(sealed, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(opened) != 0 {
		t.Fatalf("len(opened) = %d, want 0", len(opened))
	}
}

func TestCryptoLargePlaintext(t *testing.T) {
	plaintext := bytes.Repeat([]byte("a"), 1<<20)
	start := time.Now()

	sealed, err := Seal(plaintext, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := Open(sealed, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatal("large plaintext round-trip mismatch")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("crypto round-trip took %s, want <3s", time.Since(start))
	}
}

func TestSealWriterOpenReaderRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sw, err := SealWriter(&buf, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("SealWriter: %v", err)
	}
	payload := bytes.Repeat([]byte("streamed portion "), 1024) // 17KB, spans <1 chunk
	if _, err := sw.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sr, err := OpenReader(bytes.NewReader(buf.Bytes()), []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	got, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("streaming round-trip mismatch")
	}
}

func TestSealWriterMultipleChunks(t *testing.T) {
	var buf bytes.Buffer
	sw, err := SealWriter(&buf, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("SealWriter: %v", err)
	}
	// Write 40MB to force at least 2 chunks (default 16MB chunk size).
	payload := bytes.Repeat([]byte{0x5a}, 40*1024*1024)
	if _, err := sw.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sr, err := OpenReader(bytes.NewReader(buf.Bytes()), []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	got, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("multi-chunk streaming round-trip mismatch")
	}
}

func TestSealWriterRejectsWritesAfterClose(t *testing.T) {
	var buf bytes.Buffer
	sw, err := SealWriter(&buf, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("SealWriter: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = sw.Write([]byte("late"))
	if err == nil {
		t.Fatal("expected error on write after close")
	}
}

func TestOpenReaderRejectsBadVersion(t *testing.T) {
	// Fabricate a bundle with version byte 0x01 (unsupported).
	raw := make([]byte, 0, 40)
	raw = append(raw, []byte(MagicHeader)...)
	raw = append(raw, 0x01) // version
	raw = append(raw, make([]byte, saltLen)...)
	raw = append(raw, make([]byte, 4)...)
	_, err := OpenReader(bytes.NewReader(raw), []byte("strong-passphrase"))
	if err == nil {
		t.Fatal("expected version-mismatch error")
	}
}

func TestOpenReaderDetectsMidStreamTamper(t *testing.T) {
	var buf bytes.Buffer
	sw, err := SealWriter(&buf, []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("SealWriter: %v", err)
	}
	// 40MB → 2+ chunks. Tamper last byte of the stream (tail of final chunk).
	if _, err := sw.Write(bytes.Repeat([]byte{0xa5}, 40*1024*1024)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw := buf.Bytes()
	raw[len(raw)-1] ^= 0xFF

	sr, err := OpenReader(bytes.NewReader(raw), []byte("strong-passphrase"))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	if _, err := io.ReadAll(sr); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("ReadAll error = %v, want ErrBadPassphrase", err)
	}
}

func TestValidatePassphrase(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		warning, fatal := ValidatePassphrase([]byte("1234567"))
		if warning != nil {
			t.Fatalf("warning = %v, want nil", warning)
		}
		if fatal == nil {
			t.Fatal("fatal = nil, want error")
		}
	})

	t.Run("weak but allowed", func(t *testing.T) {
		warning, fatal := ValidatePassphrase([]byte("123456789"))
		if warning == nil {
			t.Fatal("warning = nil, want warning")
		}
		if fatal != nil {
			t.Fatalf("fatal = %v, want nil", fatal)
		}
	})

	t.Run("strong enough", func(t *testing.T) {
		warning, fatal := ValidatePassphrase([]byte("123456789012"))
		if warning != nil || fatal != nil {
			t.Fatalf("warning=%v fatal=%v, want both nil", warning, fatal)
		}
	})
}
