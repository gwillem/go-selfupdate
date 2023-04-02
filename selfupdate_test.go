package selfupdate

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUpdateRestart(t *testing.T) {
	if os.Getenv(restartEnvVar) != "" {
		fmt.Println("We are in the restarted process! (TestUpdateRestart")
	} else {
		fmt.Println("Running UpdateRestart func...")
	}

	// run test http server, which returns current executable
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("http test server got request!")
		self, err := os.Executable()
		if err != nil {
			panic(err)
		}
		src, err := os.Open(self)
		if err != nil {
			panic(err)
		}
		defer src.Close()

		// send self
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, src)
	}))
	defer ts.Close()

	// Set to past, or we would skip updating because we are too new
	pastTS := time.Now().Add(-10 * time.Second)
	selfExe, _ := executable()
	assert.NoError(t, os.Chtimes(selfExe, pastTS, pastTS))

	ok, err := UpdateRestart(ts.URL)
	assert.NoError(t, err) // doesn't return the first call, but does the second
	assert.True(t, ok)     // in new process
}

func TestAge(t *testing.T) {
	path, err := executable()
	assert.NoError(t, err)

	myAge := age(path)
	if myAge < 0 {
		t.Errorf("age() returned negative value: %s", myAge)
	}
	if myAge > 15*time.Second {
		t.Errorf("age() returned too big value: %s", myAge)
	}
}
