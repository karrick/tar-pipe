package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/karrick/godirwalk"
	"github.com/karrick/golf"
)

var (
	optHelp    = golf.BoolP('h', "help", false, "print help then exit")
	optVerbose = golf.BoolP('v', "verbose", false, "print verbose information")
	optZip     = golf.BoolP('z', "gzip", false, "(de-)compress with gzip")
)

func main() {
	golf.Parse()

	if *optHelp {
		fmt.Fprintf(os.Stderr, "%s\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "        optimized transfer of directory contents over TCP socket\n\n")
		fmt.Fprintf(os.Stderr, "Copies one or more file system entries from source to destination over a\n")
		fmt.Fprintf(os.Stderr, "network socket. While `rsync` is the preferred choice for this particular task\n")
		fmt.Fprintf(os.Stderr, "when synchronizing files, when copying files for the first time, `tar-pipe` is\n")
		fmt.Fprintf(os.Stderr, "much faster.\n\n")
		fmt.Fprintf(os.Stderr, "Always start `tar-pipe` on the destination first:\n\ttar-pipe -vz receive :6969\n\n")
		fmt.Fprintf(os.Stderr, "Then on source:\n\ttar-pipe -vz send destination.example.com:6969 dir1 dir2 file3...\n\n")
		golf.Usage()
		exit(nil)
	}

	args := golf.Args()
	if len(args) == 0 {
		usage("expected sub-command")
	}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "receive":
		exit(receive(args))
	case "send":
		exit(send(args))
	default:
		usage(fmt.Sprintf("invalid sub-command: %q", cmd))
	}
}

func exit(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func usage(message string) {
	fmt.Fprintf(os.Stderr, "%s\nusage: %s [receive $binding_address | send $destination_address]\n", message, filepath.Base(os.Args[0]))
	os.Exit(2)
}

func verbose(format string, a ...interface{}) {
	if *optVerbose {
		_, _ = fmt.Fprintf(os.Stdout, format, a...)
	}
}

func warning(format string, a ...interface{}) {
	if *optVerbose {
		_, _ = fmt.Fprintf(os.Stderr, format, a...)
	}
}

func withGzipReader(use bool, ior io.Reader, callback func(ior io.Reader) error) error {
	if !use {
		return callback(ior)
	}
	verbose("# Using gzip compression\n")
	z, err := gzip.NewReader(ior)
	if err != nil {
		return err
	}
	err = callback(z)
	if err2 := z.Close(); err == nil {
		err = err2
	}
	return err
}

func withGzipWriter(use bool, iow io.Writer, callback func(iow io.Writer) error) error {
	if !use {
		return callback(iow)
	}
	verbose("# Using gzip compression\n")
	z := gzip.NewWriter(iow)
	err := callback(z)
	if err2 := z.Close(); err == nil {
		err = err2
	}
	return err
}

func withDial(remote string, callback func(iow io.Writer) error) error {
	conn, err := net.Dial("tcp", remote)
	if err != nil {
		return err
	}
	verbose("# Connected: %q\n", conn.RemoteAddr())

	err = withGzipWriter(*optZip, conn, func(iow io.Writer) error {
		return callback(iow)
	})

	if err2 := conn.Close(); err == nil {
		err = err2
	}
	return err
}

func withListen(bind string, callback func(ior io.Reader) error) error {
	l, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	verbose("# Listening: %q\n", bind)
	conn, err := l.Accept()
	if err != nil {
		return err
	}
	verbose("# Accepted connection: %q\n", conn.RemoteAddr())

	err = withGzipReader(*optZip, conn, func(ior io.Reader) error {
		return callback(ior)
	})

	if err2 := conn.Close(); err == nil {
		err = err2
	}
	return err
}

// dirBlurb holds the name and modification time of a directory entry.
type dirBlurb struct {
	Name    string
	ModTime time.Time
}

func receive(operands []string) error {
	if len(operands) < 1 {
		usage(fmt.Sprintf("cannot receive without binding address"))
	}
	return withListen(operands[0], func(ior io.Reader) error {
		var directories []dirBlurb

		buf := make([]byte, 64*1024)

		tr := tar.NewReader(ior)
		for {
			th, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			dirname := filepath.Dir(th.Name)
			if err = os.MkdirAll(dirname, os.ModePerm); err != nil {
				return err
			}

			switch th.Typeflag {
			case tar.TypeDir:
				_, err = os.Stat(th.Name)
				if err == nil {
					// TODO: what if entry is not a directory?
					if err = os.Chmod(th.Name, os.FileMode(th.Mode)); err != nil {
						return err
					}
				} else if os.IsNotExist(err) {
					if err = os.Mkdir(th.Name, os.FileMode(th.Mode)); err != nil {
						return err
					}
				}
				// Cannot set the mtime of a directory entry now, but must do so
				// after we process all the child entries in that directory. For
				// now, we'll store a bit of information that we can use later
				// to set the mtime for the directory.
				directories = append(directories, dirBlurb{th.Name, th.ModTime})
			case tar.TypeLink:
				if err = os.Link(th.Linkname, th.Name); err != nil {
					return err
				}
				if err = os.Chtimes(th.Name, th.ModTime, th.ModTime); err != nil {
					return err
				}
			case tar.TypeSymlink:
				if err = os.Symlink(th.Linkname, th.Name); err != nil {
					return err
				}
				// ??? Chtimes does not seem to work on a symlink
			case tar.TypeFifo:
				if err = makeFIFO(th, tr, buf); err != nil {
					return err
				}
			default:
				// TODO: support tar.TypeBlock
				// TODO: support tar.TypeChar
				if err = makeRegular(tr, th, buf); err != nil {
					return err
				}
			}
		}

		// Walk list of directories backwards, to ensure modification times are
		// not updated by later updates deeper inside a directory
		// location. Because program will send /foo through the pipe before
		// /foo/bar, a reverse of the directory order will ensure we update the
		// modification time for /foo/bar before we update the modification time
		// for /foo.
		for i := len(directories) - 1; i >= 0; i-- {
			de := directories[i]
			if err := os.Chtimes(de.Name, de.ModTime, de.ModTime); err != nil {
				return err
			}
		}

		return nil
	})
}

func makeRegular(tr *tar.Reader, th *tar.Header, buf []byte) error {
	// NOTE: Any other file type, including tar.TypeReg, ought to be written as
	// a regular file, to be inspected by user.
	tempName := th.Name + ".partial"
	fh, err := os.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(th.Mode))
	if err != nil {
		return err
	}
	nc, err := io.CopyBuffer(fh, tr, buf)
	if err != nil {
		return err
	}
	if err = fh.Close(); err != nil {
		return err
	}
	if nc != th.Size {
		return fmt.Errorf("mis-write: %d written, expected: %d", nc, th.Size)
	}
	if err = os.Rename(tempName, th.Name); err != nil {
		return err
	}
	return os.Chtimes(th.Name, th.ModTime, th.ModTime)
}

// it would seem send transmits a format that native tar cannot decode

func send(operands []string) error {
	if len(operands) < 1 {
		usage(fmt.Sprintf("cannot send without destination address"))
	}
	return withDial(operands[0], func(iow io.Writer) error {
		var err error
		tw := tar.NewWriter(iow)
		if len(operands) == 1 {
			operands = append(operands, ".")
		}
		buf := make([]byte, 64*1024)
		for _, operand := range operands[1:] {
			if err = tarpath(tw, operand, buf); err != nil {
				return err
			}
		}
		if err2 := tw.Close(); err == nil {
			err = err2
		}
		return err
	})
}

func tarpath(tw *tar.Writer, osPathname string, buf []byte) error {
	fi, err := os.Stat(osPathname)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return tarnode(tw, osPathname, buf)
	}
	return godirwalk.Walk(osPathname, &godirwalk.Options{
		Callback: func(osPathname string, _ *godirwalk.Dirent) error {
			return tarnode(tw, osPathname, buf)
		},
		ErrorCallback: func(osPathname string, err error) godirwalk.ErrorAction {
			warning("%s: %s\n", osPathname, err)
			return godirwalk.SkipNode
		},
		ScratchBuffer: make([]byte, 64*1024),
		Unsorted:      true,
	})
}

func tarnode(tw *tar.Writer, osPathname string, buf []byte) error {
	fi, err := os.Lstat(osPathname)
	if err != nil {
		return err
	}

	mode := fi.Mode()

	th := &tar.Header{
		ModTime: fi.ModTime(),
		Mode:    int64(mode),
		Name:    osPathname,
	}

	if mode&os.ModeDir != 0 {
		th.Typeflag = tar.TypeDir
		return tw.WriteHeader(th)
	}

	if mode&os.ModeSymlink != 0 {
		referent, err := os.Readlink(osPathname)
		if err != nil {
			return err
		}
		th.Linkname = referent
		th.Typeflag = tar.TypeSymlink
		return tw.WriteHeader(th)
	}

	if mode&os.ModeNamedPipe /* FIFO */ != 0 {
		th.Typeflag = tar.TypeFifo
		return tw.WriteHeader(th)
	}

	if !mode.IsRegular() {
		// At this point, if there are any remaining file mode bits, they are
		// not supported, and ought to be skipped with an appropriate error
		// message.
		//
		// os.ModeSocket (unix domain sockets) is not supported because tar
		// format does not provide means of classifying it.
		//
		// os.ModeDevice (including os.ModeCharDevice) is not supported because
		// I do not have a method of getting the major and minor device numbers
		// of a file system entry without calling C.
		warning("%s not supported: %s\n", mode, osPathname)
		return nil
	}

	// NOTE: There is no os library mode type for hard link, because every hard
	// link is equal to each other hard link. Discovering whether a particular
	// node is a hard link with another file in the same file system is an
	// O(n^2) problem, and not solved here.

	th.Size = int64(fi.Size())
	th.Typeflag = tar.TypeReg
	if err := tw.WriteHeader(th); err != nil {
		return err
	}
	fh, err := os.Open(osPathname)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(tw, fh, buf)
	if err2 := fh.Close(); err == nil {
		err = err2
	}
	return err
}
