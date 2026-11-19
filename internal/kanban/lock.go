package kanban

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrLocked is Lock's error when another desk has the board.
var ErrLocked = errors.New("another desk is running with this board")

// Lock takes the board at path for this process: two desks on one board
// would each save their own copy over the other's. It holds until the file
// it returns is closed or the process ends, a crash too (the OS lets go).
func Lock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
