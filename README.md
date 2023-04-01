# Golang self-update from URL 

Runs a cheap auto-update using `Last-Modified-If` header, so only downloads when a newer version is available (based on modification timestamp of the current executable).

```
import (
    "fmt"
    "github.com/gwillem/go-selfupdate"
)

var updateURL = "https://yoursite/gobinary"

func main() {
    if ok, _ := selfupdate.UpdateRestart(updateURL); ok {
        fmt.Println("New version reporting!")
    }
}
```

Because `UpdateRestart` will replace the current process, you should run at the beginning of your code (such as in `init()`).
