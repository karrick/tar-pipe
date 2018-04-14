# tar-pipe

tar-pipe optimized transfer of directory contents over TCP socket.

## Description

tar-pipe is a very fast way to copy one or more directory hierarchies
from one machine to another over a network socket. While rsync is the
preferred choice for this particular task when synchronizing files,
when copying files for first time, tar-pipe is much faster.

tar-pipe can be configured to compress the data using gzip
compression.

### Performance and Reliability

rsync is an amazing and fast program. I recommend anyone curious to
study its source code and learn how it works. However, for making a
bulk filesystem copy across a network, some of the features of rsync
make it more slow than merely streaming the file system object data
and metadata across the network.

I thouroughly tested tar-pipe multiple times with nearly 900 GiB of
data, comparing the transfer time and the resultant file system
output. To test the speed, I simply invoked each command using the
`time` shell builtin command to measure the wall clock time to
transfer the ~900 GiB file system hierarchy.

On the low-end embedded devices I tested on, tar-pipe repeatedly
performed the transfer in ~12 minutes, and rsync repeatedly performed
the same transfer to an empty directory in ~45 minutes.

To test the correctness of tar-pipe, after I ran tar-pipe to transfer
the ~900 GiB file system hierarchy, I ran rsync in verbose mode to
display any changes needed to make the destination a duplicate of the
source. In every test, the only differences were the file system
modification times of the destination directories. These differences
are due to the algorithm that tar-pipe uses that make it much faster
than rsync for the bulk-transfer. This limitation is duscussed below
in the Limitations section.

## Usage

Always start tar-pipe on the destination machine first. The receive
subcommand expects an optional IP address and a mandatory port number
to bind to. The port number must always be proceeded by the colon
character. The optional IP address would be used when tar-pipe ought
to bind only to a particular interface, if desired.

    [you@destination.example.com ~]$ tar-pipe receive :6969

After the recipient is waiting, send the files from the source.

    [you@source.example.com ~]$ tar-pipe send destination.example.com:6969 dir1 dir2 ...

After tar-pipe finishes, ~/dir1 and ~/dir2 from source.example.com
will be replicated to ~/dir1 and ~/dir2 on destination.example.com.

### Compression

By default tar-pipe sends the raw tar stream without compression. To
sacrifice some CPU overhead to acheive better network throughput,
tar-pipe will compress the tar stream using gzip when the `-z, --gzip`
command line flags are provided.

*NOTE:* Both the sender and receiver must be invoked with the
compression flag.

On the destination host:

    [you@destination.example.com ~]$ tar-pipe -z receive :6969

On the source host:

    [you@source.example.com ~]$ tar-pipe -z send destination.example.com:6969 dir1 dir2 ...

### Verbose Output

By default tar-pipe does not display any output on the source or
destination hosts unless any errors are encountered, in which case
errors are printed to standard error. When tar-pipe is invoked with
the `-v, --verbose` command line flag, it displays connection
information, compression information. The verbose flag on the source
and destination are independent of each other.

    [you@destination.example.com ~]$ tar-pipe -d receive :6969

## Limitations

The following file system objects are not yet implemented:

1. Hard links (symbolic links _are_ supported)
1. Character devices
1. Block devices
1. FIFOs

While all file system objects on the destination host will have the
same meta data as they have on the source host, tar-pipe does not set
the modification time of directories to match the modification time of
the corresponding directories on the source host. This limitation
would be simple to change, but could slow tar-pipe down as it would
have to process the tar stream differently. I am considering different
ways of doing this without the performance penalty.
