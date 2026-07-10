package tracecheck

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// atomicOutput holds a descriptor-backed directory root across writes. On
// supported platforms, later chdir calls, parent-directory renames, or parent
// symlink swaps cannot redirect the final output into a control artifact.
type atomicOutput struct {
	root *os.Root
	name string
	path string
}

func openAtomicOutput(path string) (*atomicOutput, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute = filepath.Clean(absolute)
	dir := filepath.Dir(absolute)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &atomicOutput{root: root, name: filepath.Base(absolute), path: absolute}, nil
}

func (o *atomicOutput) Close() error {
	if o == nil || o.root == nil {
		return nil
	}
	err := o.root.Close()
	o.root = nil
	return err
}

func (o *atomicOutput) WriteFile(data []byte, creationPerm fs.FileMode) (err error) {
	perm := creationPerm
	if info, statErr := o.root.Lstat(o.name); statErr == nil {
		// os.WriteFile historically preserved the mode of an existing regular
		// destination. Do not inherit through a symlink: it will be replaced.
		if info.Mode().IsRegular() {
			perm = info.Mode().Perm()
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	tmp, tmpName, err := o.createTemp()
	if err != nil {
		return err
	}
	defer func() {
		if tmpName == "" {
			return
		}
		removeErr := o.root.Remove(tmpName)
		if err == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			err = fmt.Errorf("remove temporary output %s: %w", tmpName, removeErr)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	written, err := tmp.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if syncErr := tmp.Sync(); err == nil && syncErr != nil {
		err = syncErr
	}
	if closeErr := tmp.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := o.root.Rename(tmpName, o.name); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

func (o *atomicOutput) createTemp() (*os.File, string, error) {
	for range 100 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := "." + o.name + ".tmp-" + hex.EncodeToString(random[:])
		file, err := o.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		return file, name, err
	}
	return nil, "", fmt.Errorf("create temporary output for %s: too many name collisions", o.path)
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	output, err := openAtomicOutput(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	return output.WriteFile(data, perm)
}
