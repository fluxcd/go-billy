//go:build !js
// +build !js

// Copyright (C) 2014-2015 Docker Inc & Go Authors. All rights reserved.
// Copyright (C) 2017 SUSE LLC. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package util

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// IsNotExist tells you if err is an error that implies that either the path
// accessed does not exist (or path components don't exist). This is
// effectively a more broad version of os.IsNotExist.
func IsNotExist(err error) bool {
	// Check that it's not actually an ENOTDIR, which in some cases is a more
	// convoluted case of ENOENT (usually involving weird paths).
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ENOENT)
}

// SecureJoinVFS joins the two given path components (similar to Join) except
// that the returned path is guaranteed to be scoped inside the provided root
// path (when evaluated). Any symbolic links in the path are evaluated with the
// given root treated as the root of the filesystem, similar to a chroot. The
// filesystem state is evaluated through the given VFS interface (if nil, the
// standard os.* family of functions are used).
//
// Note that the guarantees provided by this function only apply if the path
// components in the returned string are not modified (in other words are not
// replaced with symlinks on the filesystem) after this function has returned.
// Such a symlink race is necessarily out-of-scope of SecureJoin.
func SecureJoinVFS(root, unsafePath string, vfs VFS) (string, error) {
	// Use the os.* VFS implementation if none was specified.
	if vfs == nil {
		vfs = osVFS{}
	}

	var path bytes.Buffer
	n := 0
	for unsafePath != "" {
		if n > 255 {
			return "", &os.PathError{Op: "SecureJoin", Path: root + "/" + unsafePath, Err: syscall.ELOOP}
		}

		// Next path component, p.
		i := strings.IndexRune(unsafePath, filepath.Separator)
		var p string
		if i == -1 {
			p, unsafePath = unsafePath, ""
		} else {
			p, unsafePath = unsafePath[:i], unsafePath[i+1:]
		}

		// Create a cleaned path, using the lexical semantics of /../a, to
		// create a "scoped" path component which can safely be joined to fullP
		// for evaluation. At this point, path.String() doesn't contain any
		// symlink components.
		cleanP := filepath.Clean(string(filepath.Separator) + path.String() + p)
		if cleanP == string(filepath.Separator) {
			path.Reset()
			continue
		}
		fullP := filepath.Clean(root + cleanP)

		// Figure out whether the path is a symlink.
		fi, err := vfs.Lstat(fullP)
		if err != nil && !IsNotExist(err) {
			return "", err
		}
		// Treat non-existent path components the same as non-symlinks (we
		// can't do any better here).
		if IsNotExist(err) || fi.Mode()&os.ModeSymlink == 0 {
			path.WriteString(p)
			path.WriteRune(filepath.Separator)
			continue
		}

		// Only increment when we actually dereference a link.
		n++

		// It's a symlink, expand it by prepending it to the yet-unparsed path.
		dest, err := vfs.Readlink(fullP)
		if err != nil {
			return "", err
		}
		// Absolute symlinks reset any work we've already done.
		if filepath.IsAbs(dest) {
			// Change from upstream, to avoid duplicating root dir.
			if !fi.IsDir() && strings.HasPrefix(dest, root+string(filepath.Separator)) {
				return filepath.Clean(dest), nil
			}
			path.Reset()
		}
		unsafePath = dest + string(filepath.Separator) + unsafePath
	}

	// We have to clean path.String() here because it may contain '..'
	// components that are entirely lexical, but would be misleading otherwise.
	// And finally do a final clean to ensure that root is also lexically
	// clean.
	fullP := filepath.Clean(string(filepath.Separator) + path.String())
	return filepath.Clean(root + fullP), nil
}

// SecureJoin is a wrapper around SecureJoinVFS that just uses the os.* library
// of functions as the VFS. If in doubt, use this function over SecureJoinVFS.
func SecureJoin(root, unsafePath string) (string, error) {
	return SecureJoinVFS(root, unsafePath, nil)
}

// In future this should be moved into a separate package, because now there
// are several projects (umoci and go-mtree) that are using this sort of
// interface.

// VFS is the minimal interface necessary to use SecureJoinVFS. A nil VFS is
// equivalent to using the standard os.* family of functions. This is mainly
// used for the purposes of mock testing, but also can be used to otherwise use
// SecureJoin with VFS-like system.
type VFS interface {
	// Lstat returns a FileInfo describing the named file. If the file is a
	// symbolic link, the returned FileInfo describes the symbolic link. Lstat
	// makes no attempt to follow the link. These semantics are identical to
	// os.Lstat.
	Lstat(name string) (os.FileInfo, error)

	// Readlink returns the destination of the named symbolic link. These
	// semantics are identical to os.Readlink.
	Readlink(name string) (string, error)
}

// osVFS is the "nil" VFS, in that it just passes everything through to the os
// module.
type osVFS struct{}

// Lstat returns a FileInfo describing the named file. If the file is a
// symbolic link, the returned FileInfo describes the symbolic link. Lstat
// makes no attempt to follow the link. These semantics are identical to
// os.Lstat.
func (o osVFS) Lstat(name string) (os.FileInfo, error) { return os.Lstat(name) }

// Readlink returns the destination of the named symbolic link. These
// semantics are identical to os.Readlink.
func (o osVFS) Readlink(name string) (string, error) { return os.Readlink(name) }
