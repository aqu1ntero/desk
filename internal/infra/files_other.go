//go:build !windows

package infra

import (
	"context"

	"github.com/pkg/sftp"
)

// localSFTP is none: the local host's files go through sh scripts here
// (files_windows.go serves them over sftp in-process).
func localSFTP(context.Context) (*sftp.Client, func(), error) { return nil, nil, nil }

// localPath is p: the local host's paths are this OS's own.
func localPath(p string) string { return p }
