//go:build !windows

package bundle

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func processElevated() bool {
	return os.Geteuid() == 0
}

// createStageDir creates dir readable and writable by its owner only, failing
// with an fs.ErrExist error if anything is already there.
func createStageDir(dir string) error {
	return os.Mkdir(dir, 0700)
}

// checkStageDir accepts dir only if it is a real directory owned by root or
// this user that nobody else can write.
func checkStageDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("it is a symbolic link")
	}
	if !info.IsDir() {
		return errors.New("it is not a directory")
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("its mode %v lets others write it", info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("it is owned by uid %d", st.Uid)
	}
	return nil
}
