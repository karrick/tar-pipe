package main

import (
	"bufio"
	"testing"

	"github.com/karrick/gorill"
	"golang.org/x/crypto/bcrypt"
)

func key32(tb testing.TB, passphrase string) [32]byte {
	kk, err := bcrypt.GenerateFromPassword([]byte(passphrase), 14)
	if err != nil {
		tb.Fatal(err)
	}
	var key [32]byte
	copy(key[:], kk[:32])
	return key
}

func TestEncryption(t *testing.T) {
	key := key32(t, "test-passphrase")

	cipherstream := gorill.NewNopCloseBuffer()
	se, err := NewEncryptor(cipherstream, key)
	if err != nil {
		t.Fatal(err)
	}

	for _, item := range []string{"one\n", "two\n", "three\n", "four\n"} {
		nw, err := se.Write([]byte(item))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := nw, len(item); got != want {
			t.Fatalf("ITEM: %q; GOT: %v; WANT: %d", item, got, want)
		}
	}
	if err = se.Close(); err != nil {
		t.Fatal(err)
	}

	sd, err := NewDecryptor(cipherstream, key)
	if err != nil {
		t.Fatal(err)
	}

	var output []string
	scanner := bufio.NewScanner(sd)
	for scanner.Scan() {
		output = append(output, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if got, want := len(output), 4; got != want {
		t.Fatalf("GOT: %v; WANT: %v", got, want)
	}
	if got, want := output[0], "one"; got != want {
		t.Fatalf("GOT: %q; WANT: %q", got, want)
	}
	if got, want := output[1], "two"; got != want {
		t.Fatalf("GOT: %q; WANT: %q", got, want)
	}
	if got, want := output[2], "three"; got != want {
		t.Fatalf("GOT: %q; WANT: %q", got, want)
	}
}
