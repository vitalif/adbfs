package goadb

import (
	"fmt"
	"os"
	"time"

	"github.com/zach-klippenstein/goadb/wire"
)

// DirEntry holds information about a directory entry on a device.
type DirEntry struct {
	Name       string
	Mode       os.FileMode
	Size       int32
	ModifiedAt time.Time
}

// DirEntries iterates over directory entries.
type DirEntries struct {
	scanner wire.SyncScanner

	currentEntry *DirEntry
	err          error
}

func (entries *DirEntries) Next() bool {
	if entries.err != nil {
		return false
	}

	entry, done, err := readNextDirListEntry(entries.scanner)
	if err != nil {
		entries.err = err
		entries.Close()
		return false
	}

	entries.currentEntry = entry
	if done {
		entries.Close()
		return false
	}

	return true
}

func (entries *DirEntries) Entry() *DirEntry {
	return entries.currentEntry
}

func (entries *DirEntries) Err() error {
	return entries.err
}

// Close closes the connection to the adb.
// Next() will call Close() before returning false.
func (entries *DirEntries) Close() error {
	return entries.scanner.Close()
}

func readNextDirListEntry(s wire.SyncScanner) (entry *DirEntry, done bool, err error) {
	id, err := s.ReadOctetString()
	if err != nil {
		return
	}

	if id == "DONE" {
		done = true
		return
	} else if id != "DENT" {
		err = fmt.Errorf("error reading dir entries: expected dir entry ID 'DENT', but got '%s'", id)
		return
	}

	mode, err := s.ReadFileMode()
	if err != nil {
		err = fmt.Errorf("error reading dir entries: error reading file mode: %v", err)
		return
	}
	size, err := s.ReadInt32()
	if err != nil {
		err = fmt.Errorf("error reading dir entries: error reading file size: %v", err)
		return
	}
	mtime, err := s.ReadTime()
	if err != nil {
		err = fmt.Errorf("error reading dir entries: error reading file time: %v", err)
		return
	}
	name, err := s.ReadString()
	if err != nil {
		err = fmt.Errorf("error reading dir entries: error reading file name: %v", err)
		return
	}

	done = false
	entry = &DirEntry{
		Name:       name,
		Mode:       mode,
		Size:       size,
		ModifiedAt: mtime,
	}
	return
}