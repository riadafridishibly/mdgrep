// Package walk finds the markdown a search should read: the files named
// outright, and the files under the directories named, in the order a single
// reader of the tree would have met them.
package walk

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/riadafridishibly/mdgrep/internal/ignore"
)

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	".venv": true, "__pycache__": true, "target": true,
}

// Files reads paths in the order they were given and returns the markdown
// under them, plus whether the caller should also read stdin: no paths at all
// with something piped in, or "-" named among them.
func Files(paths []string, exts map[string]bool, hidden, noIgnore bool) ([]string, bool, error) {
	useStdin := false
	if len(paths) == 0 {
		if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			return nil, true, nil
		}
		paths = []string{"."}
	}

	var files []string
	seen := map[string]bool{}
	c := collector{exts: exts, hidden: hidden, noIgnore: noIgnore, tokens: make(chan struct{}, walkers)}
	for _, p := range paths {
		if p == "-" {
			useStdin = true
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, false, err
		}
		if !info.IsDir() {
			if !seen[p] {
				seen[p] = true
				files = append(files, p)
			}
			continue
		}
		var m *ignore.Matcher
		if !noIgnore {
			m = ignore.New(p)
		}
		// The root was asked for by name, so it is searched whatever the rules
		// above it say; everything below it is not.
		var root node
		c.walk(p, m.Root(), &root)
		c.wg.Wait()
		files = root.flatten(seen, files)
	}
	return files, useStdin, nil
}

// walkers bounds how many directories are being read at once. Four is where
// the measurements stop improving: on a 10,000-directory tree the wall clock
// is the same anywhere from three walkers upward, but the kernel time doubles
// on the way from four to eight. The walk waits on the filesystem rather than
// on a core, and the filesystem stops going faster long before the cores run
// out.
var walkers = min(runtime.NumCPU(), 4)

// collector walks a directory tree and keeps the files worth searching. It
// reads each directory itself rather than going through filepath.WalkDir,
// because the listing is also how the ignore rules find their own files, and
// because reading one directory is not work a single goroutine should be doing
// alone.
type collector struct {
	exts     map[string]bool
	hidden   bool
	noIgnore bool
	tokens   chan struct{}
	wg       sync.WaitGroup
}

// node is one directory's share of the walk, in listing order: each child is
// either a file worth searching or the node of a subdirectory that was
// descended into. Keeping the shape of the tree is what lets the branches be
// walked at once and still come back in the order one goroutine would have
// found them in.
type node struct {
	file string
	kids []*node
}

// walk fills n with what dir holds, descending in parallel where there is a
// goroutine to spare and in place where there is not. Only the goroutine that
// read a directory appends to that directory's node, and every node is
// complete before the walk's WaitGroup falls to zero, so flatten reads a tree
// nobody is still writing to.
func (c *collector) walk(dir string, f ignore.Frame, n *node) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	f = f.Enter(dir, entries)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if (!c.noIgnore && skipDirs[name]) || (!c.hidden && strings.HasPrefix(name, ".")) {
				continue
			}
			if f.Excluded(name, true) {
				continue
			}
			path := filepath.Join(dir, name)
			kid := &node{}
			n.kids = append(n.kids, kid)
			select {
			case c.tokens <- struct{}{}:
				c.wg.Add(1)
				go func() {
					defer c.wg.Done()
					defer func() { <-c.tokens }()
					c.walk(path, f, kid)
				}()
			default:
				// Every token is out. Walking the subdirectory here rather
				// than waiting for one is what keeps the walk from stalling
				// on itself.
				c.walk(path, f, kid)
			}
			continue
		}
		if !c.hidden && strings.HasPrefix(name, ".") {
			continue
		}
		// Extension first: it settles most files for the price of a suffix,
		// and leaves the rules to run over the few it does not.
		if !c.exts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		if f.Excluded(name, false) {
			continue
		}
		n.kids = append(n.kids, &node{file: filepath.Join(dir, name)})
	}
}

// flatten appends the tree's files to out in walk order, dropping the ones a
// path given earlier on the command line already brought in.
func (n *node) flatten(seen map[string]bool, out []string) []string {
	if n.file != "" {
		if seen[n.file] {
			return out
		}
		seen[n.file] = true
		return append(out, n.file)
	}
	for _, kid := range n.kids {
		out = kid.flatten(seen, out)
	}
	return out
}
