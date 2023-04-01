package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gwillem/urlfilecache"
)

const restartEnvVar = "SELFUPDATE_RESTARTED_2837482372346283448"

/*

Simple library to run cheap auto updates on Go binaries.

It uses `urlfilecache` to use If-Modified-Since to check a remote server for updates

*/

func update(url string) (updated bool, err error) {
	exe, err := executable()
	if err != nil {
		return false, fmt.Errorf("Cannot determine my own executable path, skipping update: %s", err)
	}

	preTS := mtime(exe)
	if e := urlfilecache.ToCustomPath(url, exe); e != nil {
		return false, e
	}

	postTS := mtime(exe)
	if preTS != postTS {
		return true, nil // update installed
	} else {
		return false, nil // no update found
	}
}

// Replaces current executable & process with an newer one from given URL.
// Will run updated bin with same cli args and env vars.
func UpdateRestart(url string) (restarted bool, err error) {
	if os.Getenv(restartEnvVar) != "" {
		return true, nil // we are a restarted process!
	}

	updated, err := update(url)
	if err != nil {
		return false, err
	}

	if !updated {
		return false, nil
	}

	newEnv := os.Environ()
	newEnv = append(newEnv, restartEnvVar+"=1")

	if e := syscall.Exec(os.Args[0], os.Args, newEnv); e != nil {
		return false, fmt.Errorf("Could not relaunch self: %s", e)
	}
	return false, nil // never reached
}

// Implements fallback because os.Executable does not work without /proc (chroot)
func executable() (string, error) {
	exe, err := os.Executable()
	if exe != "" && err != nil {
		return exe, err
	}

	// ./binary ../../binary /abs/path/bin
	if strings.Contains(os.Args[0], "/") {
		return filepath.Abs(os.Args[0])
	}

	return exec.LookPath(os.Args[0])
}

func mtime(path string) time.Time {
	file, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return file.ModTime().UTC()
}
