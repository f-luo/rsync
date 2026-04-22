package receiver

import (
	"os"

	"github.com/gokrazy/rsync/internal/log"
	"github.com/gokrazy/rsync/internal/progress"
	"github.com/gokrazy/rsync/internal/rsyncopts"
	"github.com/gokrazy/rsync/internal/rsyncos"
	"github.com/gokrazy/rsync/internal/rsyncwire"
)

// FilterList decides whether a path is protected from --delete. An
// implementation's Match returns the outcome of the first rule that
// matched path — include=true for '+', false for '-' — or (true,
// false) if no rule matched (default-include). *sender.FilterRuleList
// satisfies it. See exclude.c:check_filter.
type FilterList interface {
	Match(path string, isDir bool) (include, matched bool)
}

// TransferOpts is a subset of Opts which is required for implementing a receiver.
type TransferOpts struct {
	Verbose  bool
	DryRun   bool
	Server   bool
	Progress bool

	DeleteMode        bool
	PreserveGid       bool
	PreserveUid       bool
	PreserveLinks     bool
	PreservePerms     bool
	PreserveDevices   bool
	PreserveSpecials  bool
	PreserveTimes     bool
	PreserveHardlinks bool
	IgnoreTimes       bool
	AlwaysChecksum    bool

	// FilterList is the exclude/include list received from the
	// peer (server-receiver) or accumulated from argv
	// (client-receiver). nil means no rules; default-include.
	FilterList FilterList

	InfoGTE  func(rsyncopts.InfoLevel, uint16) bool
	DebugGTE func(rsyncopts.DebugLevel, uint16) bool
}

type Transfer struct {
	// config
	Logger   log.Logger
	Opts     *TransferOpts
	Dest     string
	DestRoot *os.Root
	Env      *rsyncos.Env
	Progress progress.Printer

	// state
	Conn            *rsyncwire.Conn
	Seed            int32
	IOErrors        int32
	Users           map[int32]mapping
	Groups          map[int32]mapping
	retouchDirPerms bool
}

func (rt *Transfer) listOnly() bool { return rt.Dest == "" }
