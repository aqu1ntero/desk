package infra

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// localSFTP is the local host's files on Windows, which has no sh for the
// scripts: pkg/sftp's server in-process over a pipe, its paths /C:/Users/me
// (/ lists the drives), starting in the home folder.
func localSFTP(ctx context.Context) (*sftp.Client, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	a, b := net.Pipe()
	srv, err := sftp.NewServer(a, sftp.WithServerWorkingDirectory("/"+filepath.ToSlash(home)), sftp.WindowsRootEnumeratesDrives())
	if err != nil {
		return nil, nil, err
	}
	go srv.Serve()
	c, err := sftp.NewClientPipe(b, b)
	if err != nil {
		srv.Close()
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	return c, func() { stop(); c.Close(); srv.Close() }, nil
}

// localPath is a local host's sftp path (/C:/Users/me) as Windows writes
// it, so the panels' two sides compare.
func localPath(p string) string { return filepath.Clean(filepath.FromSlash(strings.TrimPrefix(p, "/"))) }
