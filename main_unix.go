// +build darwin dragonfly freebsd netbsd openbsd linux

package main

import (
	"archive/tar"
	"os"

	"golang.org/x/sys/unix"
)

func makeFIFO(th *tar.Header, _ *tar.Reader, _ []byte) error {
	err := unix.Mkfifo(th.Name, uint32(th.Mode))
	if err != nil {
		return err
	}
	return os.Chtimes(th.Name, th.ModTime, th.ModTime)
}
