package projfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every reader of a project's .yakos.yml goes through this package (K-164). A plain
// os.ReadFile of it follows a link to /dev/zero and blocks on a FIFO, in code that
// runs on every tool call. This is a tripwire for the next reader written the old
// way: it fails on a non-test Go file that opens a path named like the project file
// without projfile.
func TestNoUnboundedReadOfTheProjectFile(t *testing.T) {
	root := filepath.Join("..", "..")
	open := regexp.MustCompile(`os\.(ReadFile|Open|OpenFile)\(`)
	named := regexp.MustCompile(`(?i)yakos\.yml|ymlPath|yakosYML|projectcfg\.FileName|[^a-z]FileName\)`)
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "projfile" || d.Name() == "testdata" || d.Name() == "embedded" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for i, l := range strings.Split(string(data), "\n") {
			if open.MatchString(l) && named.MatchString(l) && !strings.HasPrefix(strings.TrimSpace(l), "//") {
				bad = append(bad, path+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(l))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("reads the project file without projfile: %s", b)
	}
}
