// Package ntfsindex keeps an index, in memory, of the files below some folders of NTFS volumes, and keeps it up to date
// from the change journal (USN) of the volume. It answers what the Explorer needs of a search engine: a search by name
// below a folder, the folders directly inside a folder with their sizes, and whether anything below a folder changed since
// a time.
//
// The index is built from the file records of the volume, not by walking the folders, and it is only held for the
// folders it was asked about (the monitored folders): the records of the rest of the volume are dropped when the build
// is done. Reading a volume needs administrator rights; without them the Manager reports that and indexes nothing.
//
// Everything that is the same on every system (the records of the journal, the tree, the queries, how a change is
// applied) is in the files without a system name and is tested with a model of a volume; volume_windows.go reads the
// real thing.
package ntfsindex

import (
	"errors"
	"time"
)

var (
	// ErrNotIndexed means the folder is not below a folder that the index was asked to hold, or it is not there (any
	// more), so the index can say nothing about it.
	ErrNotIndexed = errors.New("ntfsindex: folder is not indexed")
	// ErrNotReady means the index of the volume of the folder is being built, or could not be built.
	ErrNotReady = errors.New("ntfsindex: the index is not ready")
	// ErrJournalOff means the volume has no active change journal.
	ErrJournalOff = errors.New("ntfsindex: the volume has no active change journal")
	// ErrJournalLost means the journal no longer holds what the index read last: it was replaced, or it wrapped
	// around and overwrote the records the index had not read yet. The index has to be built again.
	ErrJournalLost = errors.New("ntfsindex: the change journal was replaced or overwritten")
	// ErrAccessDenied means the volume cannot be read without administrator rights.
	ErrAccessDenied = errors.New("ntfsindex: reading the volume needs administrator rights")
	// ErrUnsupported means the volume is not an NTFS volume of this computer, or the system cannot index it.
	ErrUnsupported = errors.New("ntfsindex: the volume cannot be indexed")
)

const (
	// frnMask keeps the number of the file record out of a file reference number: its upper 16 bits are the sequence
	// number of the record, which a file reference carries and the others do not always agree on.
	frnMask = 0x0000FFFFFFFFFFFF

	attrDirectory    = 0x10
	attrReparsePoint = 0x400

	// unixToFiletime is the FILETIME of 1970-01-01T00:00:00Z, in 100 ns units since 1601.
	unixToFiletime = 116444736000000000
)

// frnKey is what a file is known by in the index.
func frnKey(ref uint64) uint64 { return ref & frnMask }

// filetime converts a time to a Windows FILETIME.
func filetime(t time.Time) int64 { return t.UnixNano()/100 + unixToFiletime }

// fromFiletime converts a Windows FILETIME to a time; zero or an implausible value gives the zero time.
func fromFiletime(ft int64) time.Time {
	if ft < unixToFiletime {
		return time.Time{}
	}
	d := ft - unixToFiletime
	return time.Unix(d/1e7, d%1e7*100).UTC()
}

// Time converts a time of the index, a Windows FILETIME, to a time; the zero time for 0.
func Time(ft int64) time.Time { return fromFiletime(ft) }

// The reasons of a journal record (USN_REASON_*) that the index tells apart.
const (
	reasonFileCreate    = 0x00000100
	reasonFileDelete    = 0x00000200
	reasonRenameOldName = 0x00001000
	reasonRenameNewName = 0x00002000
	reasonClose         = 0x80000000
)

// usnRecord is one record of the change journal (USN_RECORD_V2), or of the enumeration of a volume.
type usnRecord struct {
	FRN, Parent uint64
	USN         int64
	// Time is when the change happened, as a FILETIME.
	Time   int64
	Reason uint32
	Attrs  uint32
	Name   string
}

// changed reports whether the record tells of a change of the file: every reason does, except the closing of the
// handle on its own.
func (r usnRecord) changed() bool { return r.Reason&^reasonClose != 0 }

// fsEntry is one name of one file as a volume lists it.
type fsEntry struct {
	FRN, Parent uint64
	// Name is the name. A volume that reads names out of a buffer leaves it empty and gives them as Raw instead, the
	// UTF-16 little-endian bytes, which are only valid until the callback returns: decoding a name that nobody wants
	// would be most of the work of reading a volume.
	Name  string
	Raw   []byte
	Attrs uint32
	// Size is the length of the unnamed data stream, MTime the time of the last write as a FILETIME.
	Size  int64
	MTime int64
}

// name returns the name of the entry, decoding it when the volume gave it as bytes.
func (e fsEntry) name() string {
	if e.Name == "" && len(e.Raw) > 0 {
		return decodeName(e.Raw)
	}
	return e.Name
}

// journalState is what the change journal of a volume says about itself.
type journalState struct {
	ID          uint64
	First, Next int64
}

// volume is what the index needs of a volume. volume_windows.go implements it on the real thing, the tests on a model.
type volume interface {
	// Journal reports the state of the change journal. ErrJournalOff when the volume has none.
	Journal() (journalState, error)
	// Dirs calls fn for every folder of the volume that is not a reparse point (a junction or a link): its file number,
	// the number of the folder it is in, and its attributes. It is the cheap first look that tells which folders are
	// inside the ones that are held.
	Dirs(fn func(frn, parent uint64, attrs uint32)) error
	// Entries calls fn for every name of every file of the volume that is not a short (8.3) name and that want says yes
	// to (it is told the file number, the number of the folder the name is in and the attributes): the entry carries the
	// size of the unnamed data stream and the time of the last write.
	Entries(want func(frn, parent uint64, attrs uint32) bool, fn func(fsEntry)) error
	// FileID returns the file reference number of the folder at path.
	FileID(path string) (uint64, error)
	// ReadJournal calls fn for the records written since from, of the journal with the given id, and returns where to
	// continue. It does not wait for new records. ErrJournalLost when from is no longer in the journal or the journal
	// was replaced.
	ReadJournal(id uint64, from int64, fn func(usnRecord)) (next int64, err error)
	// Stat returns the size, the time of the last write (a FILETIME) and the attributes of the file at path.
	Stat(path string) (size, mtime int64, attrs uint32, err error)
	// Close releases the volume.
	Close() error
}
