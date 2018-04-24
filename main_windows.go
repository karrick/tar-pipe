package main

import (
	"archive/tar"
	"fmt"
	"os"
)

func makeFIFO(th *tar.Header, tr *tar.Reader, buf []byte) error {
	fmt.Fprintf(os.Stderr, "%s extraction not supported on Windows: %s\n", th.Typeflag, th.Name)
	return makeRegular(tr, th, buf)
}
