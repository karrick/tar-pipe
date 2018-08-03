package main

import (
	"archive/tar"
	"bufio"
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

const bufferSize = 4096

var (
	key        [32]byte
	optHelp    = golf.BoolP('h', "help", false, "print help then exit")
	optSecure  = golf.BoolP('s', "secure", false, "prompt for passphrase and use symmetric key encryption")
	optVerbose = golf.BoolP('v', "verbose", false, "print verbose information")
	optZip     = golf.BoolP('z', "gzip", false, "(de-)compress with gzip")
)

func main() {
	golf.Parse()

	if *optHelp {
		fmt.Fprintf(os.Stderr, "%s\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "\toptimized transfer of file system entries over TCP socket\n\n")
		fmt.Fprintf(os.Stderr, "Copies one or more file system entries from source to destination over a\n")
		fmt.Fprintf(os.Stderr, "network socket. While `rsync` is the preferred choice for this particular task\n")
		fmt.Fprintf(os.Stderr, "when synchronizing files, when copying files for the first time, `tar-pipe` is\n")
		fmt.Fprintf(os.Stderr, "much faster.\n\n")
		fmt.Fprintf(os.Stderr, "Always start `tar-pipe` on the destination first:\n\ttar-pipe receive :6969\n\n")
		fmt.Fprintf(os.Stderr, "Then on source:\n\ttar-pipe send destination.example.com:6969 dir1 dir2 file3...\n\n")
		golf.Usage()
		exit(nil)
	}

	args := golf.Args()
	if len(args) == 0 {
		usage("expected sub-command")
	}

	if *optSecure {
		fmt.Printf("Passphrase: ")
		reader := bufio.NewReader(os.Stdin)
		passphrase, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot read input: %s", err)
			os.Exit(1)
		}
		key = Key32FromPassphrase(passphrase, passphrase)
	}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "receive":
		exit(receive(args))
	case "send":
		exit(send(args))
	case "receiveLines":
		exit(receiveLines(args))
	case "sendLines":
		exit(sendLines(args))
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
		_, _ = fmt.Fprintf(os.Stderr, "tar-pipe: "+format, a...)
	}
}

func warning(format string, a ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, "tar-pipe: "+format, a...)
}

func withDial(remote string, callback func(io.Writer) error) error {
	conn, err := net.Dial("tcp", remote)
	if err != nil {
		return err
	}
	verbose("Connected: %q\n", conn.RemoteAddr())

	err = callback(conn)
	if err2 := conn.Close(); err == nil {
		err = err2
	}
	return err
}

func withEncryptingWriter(use bool, w io.Writer, callback func(io.Writer) error) error {
	if !use {
		return callback(w)
	}
	verbose("Using AES-GCM encryption\n")

	encryptingWriter, err := NewEncryptor(w, key)
	if err != nil {
		return err
	}
	err = callback(encryptingWriter)
	if err2 := encryptingWriter.Close(); err == nil {
		err = err2
	}
	return err
}

func withCompressingWriter(use bool, w io.Writer, callback func(io.Writer) error) error {
	if !use {
		return callback(w)
	}
	verbose("Using GZIP compression\n")

	compressingWriter := gzip.NewWriter(w)
	err := callback(compressingWriter)
	if err2 := compressingWriter.Close(); err == nil {
		err = err2
	}
	return err
}

func withListen(bind string, callback func(ior io.Reader) error) error {
	l, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	verbose("Listening: %q\n", bind)

	conn, err := l.Accept()
	if err != nil {
		return err
	}
	verbose("Accepted connection: %q\n", conn.RemoteAddr())

	err = callback(conn)
	if err2 := conn.Close(); err == nil {
		err = err2
	}
	return err
}

func withDecrpytingReader(use bool, r io.Reader, callback func(io.Reader) error) error {
	if !use {
		return callback(r)
	}
	verbose("Using AES-GCM encryption\n")

	decryptingReader, err := NewDecryptor(r, key)
	if err != nil {
		return err
	}
	err = callback(decryptingReader)
	if err2 := decryptingReader.Close(); err == nil {
		err = err2
	}
	return err
}

func withDecompressingReader(use bool, r io.Reader, callback func(io.Reader) error) error {
	if !use {
		return callback(r)
	}
	verbose("Using GZIP compression\n")

	decompressingReader, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	err = callback(decompressingReader)
	if err2 := decompressingReader.Close(); err == nil {
		err = err2
	}
	return err
}

// dirBlurb holds the name and modification time of a directory entry.
type dirBlurb struct {
	Name    string
	ModTime time.Time
}

func receiveLines(operands []string) error {
	if len(operands) < 1 {
		usage(fmt.Sprintf("cannot receive without binding address"))
	}
	return withListen(operands[0], func(r io.Reader) error {
		return withDecrpytingReader(*optSecure, r, func(r io.Reader) error {
			scanner := bufio.NewScanner(r)
			for scanner.Scan() {
				fmt.Printf("RECEIVED: %s\n", scanner.Text())
			}
			return scanner.Err()
		})
	})
}

func receive(operands []string) error {
	if len(operands) < 1 {
		usage(fmt.Sprintf("cannot receive without binding address"))
	}
	return withListen(operands[0], func(r io.Reader) error {
		return withDecrpytingReader(*optSecure, r, func(r io.Reader) error {
			return withDecompressingReader(*optZip, r, func(r io.Reader) error {
				var directories []dirBlurb

				buf := make([]byte, 64*1024)

				tarReader := tar.NewReader(r)
				for {
					th, err := tarReader.Next()
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
						if err = makeFIFO(th, tarReader, buf); err != nil {
							return err
						}
					default:
						// TODO: support tar.TypeBlock
						// TODO: support tar.TypeChar
						if err = makeRegular(tarReader, th, buf); err != nil {
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
		})
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

func sendLines(operands []string) error {
	if len(operands) < 1 {
		usage(fmt.Sprintf("cannot send without destination address"))
	}
	return withDial(operands[0], func(w io.Writer) error {
		return withEncryptingWriter(*optSecure, w, func(w io.Writer) error {
			reader := bufio.NewReader(os.Stdin)
			for {
				fmt.Printf("> ")
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "[DEBUG] sending %q\n", line)
				if _, err = w.Write([]byte(line)); err != nil {
					return err
				}
			}
		})
	})
}

func send(operands []string) error {
	if len(operands) < 1 {
		usage(fmt.Sprintf("cannot send without destination address"))
	}
	return withDial(operands[0], func(w io.Writer) error {
		return withEncryptingWriter(*optSecure, w, func(w io.Writer) error {
			return withCompressingWriter(*optZip, w, func(w io.Writer) error {
				var err error
				tarWriter := tar.NewWriter(w)
				if len(operands) == 1 {
					operands = append(operands, ".")
				}
				buf := make([]byte, 64*1024)
				for _, operand := range operands[1:] {
					if err = tarpath(tarWriter, operand, buf); err != nil {
						break
					}
				}
				if err2 := tarWriter.Close(); err == nil {
					err = err2
				}
				return err
			})
		})
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
		ScratchBuffer: make([]byte, 64*1024), // own buffer to walk directory
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

	if mode&os.ModeSocket /* unix domain socket */ != 0 {
		warning("%s: tar format cannot archive socket\n", osPathname)
		return nil
	}

	if mode&os.ModeDevice /* including os.ModeCharDevice */ != 0 {
		// os.ModeDevice (including os.ModeCharDevice) is not supported because
		// I do not have a method of getting the major and minor device numbers
		// of a file system entry without calling C.
		warning("%s: cannot archive devices\n", osPathname)
		return nil
	}

	if !mode.IsRegular() {
		// At this point, if there are any remaining file mode bits, they are
		// not supported, and ought to be skipped with an appropriate error
		// message.
		warning("%s: %s not supported\n", osPathname, mode)
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
