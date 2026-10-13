package selfupdate

import (
	"debug/elf"
	"debug/macho"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/gwillem/urlfilecache"
)

const (
	restartEnvVar = "SELFUPDATE_RESTARTED_2837482372346283448"
	dialTimeout   = 3 * time.Second // fail fast when offline, such as off VPN
)

var (
	// Log receives debug output, discarded by default
	Log = log.New(io.Discard, "", log.LstdFlags)

	// ErrSkipped is returned when no update check is done on purpose,
	// such as for binaries built by "go run" or "go test"
	ErrSkipped = errors.New("selfupdate skipped")

	goBuildRegex      = regexp.MustCompile(`go-build\d+`)
	shouldCheckForDev = true
)

// download replaces exe with a newer copy from url, if any. Returns true if
// exe was replaced.
func download(url, exe string) (updated bool, err error) {
	before, err := os.Stat(exe)
	if err != nil {
		return false, err
	}

	if _, err := urlfilecache.ToPath(url,
		urlfilecache.WithPath(exe),
		urlfilecache.WithDialTimeout(dialTimeout),
		urlfilecache.WithValidator(validateExecutable),
		urlfilecache.OnlyIfNewer,
	); err != nil {
		return false, err
	}

	// urlfilecache only changes exe by renaming a new file over it
	after, err := os.Stat(exe)
	if err != nil {
		return false, err
	}
	return !os.SameFile(before, after), nil
}

func preflight() (exe string, err error) {
	exe, err = executable()
	if err != nil {
		return "", fmt.Errorf("cannot determine own executable: %w", err)
	}

	if shouldCheckForDev && isDev(exe) {
		return "", fmt.Errorf("%w: %s is a go run/test binary", ErrSkipped, exe)
	}

	return exe, nil
}

// Update replaces the current executable with a newer one from the given URL.
// Returns true if the binary was updated.
func Update(url string) (updated bool, err error) {
	exe, err := preflight()
	if err != nil {
		return false, err
	}
	return download(url, exe)
}

// UpdateRestart replaces current executable & process with a newer one from given URL.
// Will run updated bin with same cli args and env vars. Returns true in the
// restarted process.
func UpdateRestart(url string) (restarted bool, err error) {
	if os.Getenv(restartEnvVar) != "" {
		// Don't pass the marker on to child processes, which may use this package too
		if err := os.Unsetenv(restartEnvVar); err != nil {
			return true, err
		}
		return true, nil // we are a restarted process!
	}

	exe, err := preflight()
	if err != nil {
		return false, err
	}

	updated, err := download(url, exe)
	if err != nil {
		return false, err
	}

	if !updated {
		return false, nil
	}

	newEnv := os.Environ()
	newEnv = append(newEnv, restartEnvVar+"=1")

	if e := syscall.Exec(exe, os.Args, newEnv); e != nil {
		return false, fmt.Errorf("could not relaunch self: %w", e)
	}
	return false, nil // never reached
}

// executable returns the path of the running binary with symlinks resolved,
// so the update replaces the real file instead of a symlink to it.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		// os.Executable does not work without /proc (chroot)
		if exe, err = fromArgs(); err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(exe)
}

func fromArgs() (string, error) {
	// ./binary ../../binary /abs/path/bin
	if strings.Contains(os.Args[0], "/") {
		return filepath.Abs(os.Args[0])
	}
	return exec.LookPath(os.Args[0])
}

func isDev(exe string) bool {
	// cache dir such as /Users/willem/Library/Caches/go-build/35/358ddc6897af3c245cc388e59f60b74b1b275c7ebb753dbcef889336a76b6b27-d/casetool
	if slices.Contains(strings.Split(exe, "/"), "go-build") {
		return true
	}

	// new build such as /var/folders/tj/p1mpz8ys2wj682tkq0s17_xw0000gp/T/go-build288901995/b001/exe/casetool
	if goBuildRegex.MatchString(filepath.Dir(exe)) {
		return true
	}

	// go test binary
	if strings.HasSuffix(exe, ".test") {
		return true
	}

	Log.Println("isDev: false (does not seem to be a go test/run binary):", exe)
	return false
}

// validateExecutable checks that path is an executable for this OS and CPU,
// so a wrong download (such as an HTML error page) can't replace the binary.
func validateExecutable(path string) error {
	return validateFor(path, runtime.GOOS, runtime.GOARCH)
}

func validateFor(path, goos, goarch string) error {
	if goos == "darwin" {
		return validateMachO(path, goarch)
	}
	return validateELF(path, goarch)
}

var elfMachines = map[string]elf.Machine{
	"386":      elf.EM_386,
	"amd64":    elf.EM_X86_64,
	"arm":      elf.EM_ARM,
	"arm64":    elf.EM_AARCH64,
	"loong64":  elf.EM_LOONGARCH,
	"mips":     elf.EM_MIPS,
	"mipsle":   elf.EM_MIPS,
	"mips64":   elf.EM_MIPS,
	"mips64le": elf.EM_MIPS,
	"ppc64":    elf.EM_PPC64,
	"ppc64le":  elf.EM_PPC64,
	"riscv64":  elf.EM_RISCV,
	"s390x":    elf.EM_S390,
}

func validateELF(path, goarch string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("not an ELF executable: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if want, ok := elfMachines[goarch]; ok && f.Machine != want {
		return fmt.Errorf("executable is for %s, want %s", f.Machine, want)
	}
	return nil
}

var machoCPUs = map[string]macho.Cpu{
	"amd64": macho.CpuAmd64,
	"arm64": macho.CpuArm64,
}

func validateMachO(path, goarch string) error {
	want := machoCPUs[goarch]

	if f, err := macho.Open(path); err == nil {
		defer f.Close() //nolint:errcheck
		if f.Cpu != want {
			return fmt.Errorf("executable is for %s, want %s", f.Cpu, want)
		}
		return nil
	}

	// Universal binary
	f, err := macho.OpenFat(path)
	if err != nil {
		return fmt.Errorf("not a Mach-O executable: %w", err)
	}
	defer f.Close() //nolint:errcheck

	for _, arch := range f.Arches {
		if arch.Cpu == want {
			return nil
		}
	}
	return fmt.Errorf("universal executable has no %s slice", want)
}
