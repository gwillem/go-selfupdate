# Golang self-update from URL

Cheap self-update for a Go binary served as a static file. The executable's mtime is sent as `If-Modified-Since`, so it only downloads when the server copy is newer.

```go
import (
    "errors"
    "fmt"
    "log"
    "runtime"

    "github.com/gwillem/go-selfupdate"
)

var updateURL = "https://example.com/downloads/" + runtime.GOOS + "-" + runtime.GOARCH + "/tool"

func main() {
    // parse flags first, so users can opt out with e.g. --no-update

    restarted, err := selfupdate.UpdateRestart(updateURL)
    if err != nil && !errors.Is(err, selfupdate.ErrSkipped) {
        log.Println("self-update failed:", err)
    }
    if restarted {
        fmt.Println("New version reporting!")
    }
}
```

`UpdateRestart` replaces the running process, so call it early in `main`, before doing any work. `Update` only replaces the binary; the new version runs on the next start.

Both return `ErrSkipped` for binaries built by `go run` or `go test`.

## How it works

1. Find the running executable, with symlinks resolved.
2. Request the URL with the executable's mtime as `If-Modified-Since`. A 304 ends here.
3. If the response's `Last-Modified` is not newer than the executable, stop without reading the body. This prevents a downgrade when the server ignores `If-Modified-Since`.
4. Download to a temp file next to the executable and check that it is an executable for this OS and CPU (ELF, or Mach-O including universal binaries).
5. Rename it over the executable, with the server's `Last-Modified` as mtime.
6. `UpdateRestart` then runs the new binary with the same arguments and environment.

Downloads go through [urlfilecache](https://github.com/gwillem/urlfilecache). Connecting gives up after 3s and waiting for response headers after 30s.

## Server requirements

- Send `Last-Modified`. Without it, the update fails with an error.
- Serve one URL per OS and CPU.
- Use HTTPS: the downloaded file gets executed.
- nginx answers 304 by default only on an exact `If-Modified-Since` match. Updates still work, but a local binary that is newer than the server copy (such as a fresh build) gets a 200 whose body is discarded. `if_modified_since before;` avoids that.

## Limitations

- macOS and Linux only. Windows can't replace a running executable.
- The user needs write access to the executable's directory. Otherwise the update fails before downloading, with an error that matches `fs.ErrPermission`.
- For a symlinked install, such as Homebrew's `bin/tool -> ../Cellar/...`, the real file in `Cellar` is replaced.

## Debugging

```go
selfupdate.Log = log.New(os.Stderr, "selfupdate ", log.LstdFlags)
urlfilecache.Log = log.New(os.Stderr, "urlfilecache ", log.LstdFlags)
```
