# go-selfupdate

- Update detection: `os.SameFile` on exe before/after `urlfilecache.ToPath`. Relies on urlfilecache only changing the target via rename (never in place, never touching mtime on 304). Don't go back to mtime comparison: urlfilecache v0.0.5 touched the target on 304, which made every check look like an update.
- `TestUpdateRestart` execs the test binary; the child reruns the whole suite with `restartEnvVar` set.
- Subprocess helpers: `TestMain` runs a helper instead of the tests when `SELFUPDATE_TEST_HELPER` is set. Run them on a copy of the test binary (`copySelf`), never on the running one.
- Validator tests use `validateFor(path, goos, goarch)` with hand-built ELF/Mach-O headers, so both formats are covered on any host.
