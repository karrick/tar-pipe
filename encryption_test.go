package main

import (
	"bufio"
	"bytes"
	"testing"
)

func TestEncryption(t *testing.T) {
	key1 := Key32FromPassphrase("some-tag", "test-passphrase")
	key2 := Key32FromPassphrase("some-tag", "test-passphrase")

	if !bytes.Equal(key1[:], key2[:]) {
		t.Fatalf("GOT: %q; WANT: %q", key1, key2)
	}

	cipherstream := new(bytes.Buffer)
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
