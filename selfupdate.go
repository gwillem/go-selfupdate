package selfupdate

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/gwillem/urlfilecache"
)

const (
	restartEnvVar           = "SELFUPDATE_RESTARTED_2837482372346283448"
	noUpdateWhenYoungerThan = 5 * time.Second // to skip self updates on dev binaries etc
)

var (
	Log               = log.New(io.Discard, "", log.LstdFlags)
	goBuildRegex      = regexp.MustCompile(`go-build\d+`)
	shouldCheckForDev = true
)

func update(url string) (updated bool, err error) {
	exe, err := executable()
	if err != nil {
		return false, fmt.Errorf("cannot determine my own executable path, skipping update: %w", err)
	}

	preTS := mtime(exe)
	if _, e := urlfilecache.ToPath(url, urlfilecache.WithPath(exe)); e != nil {
		return false, e
	}

	return preTS != mtime(exe), nil
}

// UpdateRestart replaces current executable & process with a newer one from given URL.
// Will run updated bin with same cli args and env vars.
func UpdateRestart(url string) (restarted bool, err error) {
	if os.Getenv(restartEnvVar) != "" {
		return true, nil // we are a restarted process!
	}

	exe, err := executable()
	if err != nil {
		return false, fmt.Errorf("cannot determine my own executable path, skipping update: %w", err)
	}

	if age(exe) < noUpdateWhenYoungerThan {
		return false, fmt.Errorf("not checking for update, I am too new (%s)", age(exe))
	}

	if shouldCheckForDev && isDev() {
		return false, fmt.Errorf("not checking for update, I am a dev binary")
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
		return false, fmt.Errorf("could not relaunch self: %w", e)
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

func age(path string) time.Duration {
	return time.Since(mtime(path))
}

func isDev() bool {
	// cache dir such as /Users/willem/Library/Caches/go-build/35/358ddc6897af3c245cc388e59f60b74b1b275c7ebb753dbcef889336a76b6b27-d/casetool
	if slices.Contains(strings.Split(os.Args[0], "/"), "go-build") {
		return true
	}

	// new build such as /var/folders/tj/p1mpz8ys2wj682tkq0s17_xw0000gp/T/go-build288901995/b001/exe/casetool
	if goBuildRegex.MatchString(filepath.Dir(os.Args[0])) {
		return true
	}

	// go test binary
	if strings.HasSuffix(os.Args[0], ".test") {
		return true
	}

	Log.Println("isDev: false (does not seem to be a go test/run binary):", os.Args[0])
	return false
}
