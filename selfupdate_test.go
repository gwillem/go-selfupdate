package selfupdate

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helperEnv makes the test binary run a helper instead of the tests, see TestMain.
const helperEnv = "SELFUPDATE_TEST_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "executable":
		exe, err := executable()
		fmt.Printf("exe=%s err=%v\n", exe, err)
	case "update-deleted":
		self, err := os.Executable()
		if err != nil {
			panic(err)
		}
		if err := os.Remove(self); err != nil {
			panic(err)
		}
		_, err = Update("http://127.0.0.1:1/")
		fmt.Printf("err=%v\n", err)
	}
	os.Exit(0)
}

var (
	lastMod = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	older   = lastMod.Add(-24 * time.Hour)
	newer   = lastMod.Add(24 * time.Hour)
)

func selfBytes(t *testing.T) []byte {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	b, err := os.ReadFile(self)
	require.NoError(t, err)
	return b
}

// copySelf writes a copy of the test binary to a temp dir, with the given mtime.
func copySelf(t *testing.T, name string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, selfBytes(t), 0o755))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
	return path
}

// binaryServer serves body with Last-Modified lm, honouring If-Modified-Since.
func binaryServer(t *testing.T, lm time.Time, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", lm, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ignoringServer always answers 200, like nginx's default "if_modified_since
// exact" when the dates differ.
func ignoringServer(t *testing.T, lm time.Time, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runHelper(t *testing.T, bin, mode string) string {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper output:\n%s", out)
	return string(out)
}

func TestUpdateRestart(t *testing.T) {
	if os.Getenv(restartEnvVar) != "" {
		fmt.Println("We are in the restarted process! (TestUpdateRestart")
	} else {
		fmt.Println("Running UpdateRestart func...")
	}

	// Set to past, so the served copy is newer
	pastTS := time.Now().Add(-10 * time.Second)
	selfExe, err := executable()
	require.NoError(t, err)
	assert.NoError(t, os.Chtimes(selfExe, pastTS, pastTS))

	// run test http server, which returns current executable
	ts := binaryServer(t, time.Now(), selfBytes(t))

	shouldCheckForDev = false // override "go test" detection
	t.Cleanup(func() { shouldCheckForDev = true })

	ok, err := UpdateRestart(ts.URL)
	assert.NoError(t, err) // doesn't return the first call, but does the second
	assert.True(t, ok)     // in new process
}

func TestUpdateRestartClearsRestartMarker(t *testing.T) {
	t.Setenv(restartEnvVar, "1")

	restarted, err := UpdateRestart("http://127.0.0.1:1/")
	require.NoError(t, err)
	require.True(t, restarted)
	require.Empty(t, os.Getenv(restartEnvVar), "child processes must not inherit the marker")
}

func TestUpdateSkipsDevBinary(t *testing.T) {
	_, err := Update("http://127.0.0.1:1/")
	require.ErrorIs(t, err, ErrSkipped)
}

func TestUpdateReturnsErrorWhenExecutableIsGone(t *testing.T) {
	bin := copySelf(t, "selfupdate.test", time.Now())

	out := runHelper(t, bin, "update-deleted")
	require.Contains(t, out, "err=")
	require.NotContains(t, out, "err=<nil>")
}

func TestExecutableResolvesSymlink(t *testing.T) {
	bin := copySelf(t, "selfupdate.test", time.Now())
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(bin, link))
	want, err := filepath.EvalSymlinks(bin)
	require.NoError(t, err)

	out := runHelper(t, link, "executable")
	require.Equal(t, fmt.Sprintf("exe=%s err=<nil>\n", want), out)
}

func TestIsDev(t *testing.T) {
	tests := []struct {
		exe  string
		want bool
	}{
		{"/Users/x/Library/Caches/go-build/35/358ddc-d/casetool", true},
		{"/var/folders/tj/p1mp/T/go-build288901995/b001/exe/casetool", true},
		{"/tmp/selfupdate.test", true},
		{"/usr/local/bin/casetool", false},
		{"/home/x/go-builder/casetool", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, isDev(tt.exe), tt.exe)
	}
}

func TestDownloadReplacesOlderBinary(t *testing.T) {
	srv := binaryServer(t, lastMod, selfBytes(t))
	exe := copySelf(t, "tool", older)

	updated, err := download(srv.URL, exe)
	require.NoError(t, err)
	require.True(t, updated)
}

func TestDownloadReportsNotModified(t *testing.T) {
	srv := binaryServer(t, lastMod, selfBytes(t))
	exe := copySelf(t, "tool", older)
	_, err := download(srv.URL, exe)
	require.NoError(t, err)

	updated, err := download(srv.URL, exe)
	require.NoError(t, err)
	require.False(t, updated)
}

func TestDownloadKeepsNewerBinary(t *testing.T) {
	srv := ignoringServer(t, lastMod, []byte("older build"))
	exe := copySelf(t, "tool", newer)

	updated, err := download(srv.URL, exe)
	require.NoError(t, err)
	require.False(t, updated)
	require.Equal(t, selfBytes(t), mustRead(t, exe))
}

func TestDownloadRejectsNonBinary(t *testing.T) {
	srv := binaryServer(t, lastMod, []byte("<html>Not found</html>"))
	exe := copySelf(t, "tool", older)

	updated, err := download(srv.URL, exe)
	require.Error(t, err)
	require.False(t, updated)
	require.Equal(t, selfBytes(t), mustRead(t, exe))
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func writeFixture(t *testing.T, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	require.NoError(t, os.WriteFile(path, b, 0o755))
	return path
}

// elfHeader returns a minimal 64-bit little-endian ELF executable header.
func elfHeader(m elf.Machine) []byte {
	b := make([]byte, 64)
	copy(b, elf.ELFMAG)
	b[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	b[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	b[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(b[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(b[18:], uint16(m))
	binary.LittleEndian.PutUint32(b[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(b[52:], 64) // e_ehsize
	return b
}

// machoHeader returns a minimal 64-bit Mach-O executable header.
func machoHeader(cpu macho.Cpu) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[0:], macho.Magic64)
	binary.LittleEndian.PutUint32(b[4:], uint32(cpu))
	binary.LittleEndian.PutUint32(b[12:], uint32(macho.TypeExec))
	return b
}

// machoFat returns a universal binary with a minimal slice per cpu.
func machoFat(cpus ...macho.Cpu) []byte {
	const align = 12 // 4096 bytes
	b := make([]byte, 8+20*len(cpus))
	binary.BigEndian.PutUint32(b[0:], macho.MagicFat)
	binary.BigEndian.PutUint32(b[4:], uint32(len(cpus)))
	for i, cpu := range cpus {
		offset := (i + 1) << align
		arch := b[8+20*i:]
		binary.BigEndian.PutUint32(arch[0:], uint32(cpu))
		binary.BigEndian.PutUint32(arch[8:], uint32(offset))
		binary.BigEndian.PutUint32(arch[12:], 32)
		binary.BigEndian.PutUint32(arch[16:], align)
		b = append(b, make([]byte, offset-len(b))...)
		b = append(b, machoHeader(cpu)...)
	}
	return b
}

func TestValidateExecutableAcceptsSelf(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, validateExecutable(self))
}

func TestValidateFor(t *testing.T) {
	text := []byte("<html>Not found</html>")
	tests := []struct {
		name         string
		file         []byte
		goos, goarch string
		ok           bool
	}{
		{"linux elf", elfHeader(elf.EM_X86_64), "linux", "amd64", true},
		{"linux elf other cpu", elfHeader(elf.EM_AARCH64), "linux", "amd64", false},
		{"linux macho", machoHeader(macho.CpuAmd64), "linux", "amd64", false},
		{"linux text", text, "linux", "amd64", false},
		{"darwin macho", machoHeader(macho.CpuArm64), "darwin", "arm64", true},
		{"darwin macho other cpu", machoHeader(macho.CpuAmd64), "darwin", "arm64", false},
		{"darwin elf", elfHeader(elf.EM_AARCH64), "darwin", "arm64", false},
		{"darwin text", text, "darwin", "arm64", false},
		{"darwin universal", machoFat(macho.CpuAmd64, macho.CpuArm64), "darwin", "arm64", true},
		{"darwin universal other cpu", machoFat(macho.CpuAmd64), "darwin", "arm64", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFor(writeFixture(t, tt.file), tt.goos, tt.goarch)
			if tt.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
