package receiver

import (
	"os"

	"github.com/gokrazy/rsync/internal/log"
	"github.com/gokrazy/rsync/internal/rsyncfilter"
	"github.com/gokrazy/rsync/internal/rsyncos"
	"github.com/gokrazy/rsync/internal/rsyncwire"
)

// TransferOpts is a subset of Opts which is required for implementing a receiver.
type TransferOpts struct {
	Verbose bool
	DryRun  bool
	Server  bool

	DeleteMode        bool
	PreserveGid       bool
	PreserveUid       bool
	PreserveLinks     bool
	PreservePerms     bool
	PreserveDevices   bool
	PreserveSpecials  bool
	PreserveTimes     bool
	PreserveHardlinks bool

	// FilterList is the exclude/include rule list in command-line
	// order (client side) or as received over the wire (daemon
	// side). nil means no rules; all paths pass through.
	FilterList *rsyncfilter.List
}

type Transfer struct {
	// config
	Logger   log.Logger
	Opts     *TransferOpts
	Dest     string
	DestRoot *os.Root
	Env      *rsyncos.Env

	// state
	Conn     *rsyncwire.Conn
	Seed     int32
	IOErrors int32
	Users    map[int32]mapping
	Groups   map[int32]mapping
}

func (rt *Transfer) listOnly() bool { return rt.Dest == "" }
