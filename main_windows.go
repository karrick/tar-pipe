package main

import (
	"archive/tar"
)

func makeFIFO(th *tar.Header, tr *tar.Reader, buf []byte) error {
	warning("%s: Windows does not support FIFOs in the file system\n", th.Name)
	return makeRegular(tr, th, buf)
}
