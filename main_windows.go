package main

import (
	"archive/tar"
)

func makeFIFO(th *tar.Header, tr *tar.Reader, buf []byte) error {
	warning("%s extraction not supported on Windows: %s\n", th.Typeflag, th.Name)
	return makeRegular(tr, th, buf)
}
