package cli

import (
	"errors"
	"flag"
	"fmt"
)

// StageSep divides one stage of a pipeline from the next. It is read before
// the flags are, the way a shell reads a pipe before the commands around it,
// so it separates wherever it stands rather than being a value some flag
// could have taken.
const StageSep = "--then"

// Stages splits a command line into the stages of a pipeline. Each stage is a
// whole mdgrep command line of its own -- its own pattern, its own filters,
// its own selection -- and every stage but the first searches only inside the
// nodes the stage before it selected.
//
// A line with no separator in it is a single stage, which is every command
// mdgrep took before there were stages at all.
func Stages(args []string) ([][]string, error) {
	out := [][]string{{}}
	for _, a := range args {
		if a == StageSep {
			out = append(out, []string{})
			continue
		}
		out[len(out)-1] = append(out[len(out)-1], a)
	}
	if len(out) == 1 {
		return out, nil
	}
	for i, stage := range out {
		if len(stage) > 0 {
			continue
		}
		switch i {
		case 0:
			return nil, fmt.Errorf("%s narrows a search, so it wants one before it", StageSep)
		case len(out) - 1:
			return nil, fmt.Errorf("%s hands its nodes to another search, so it wants one after it", StageSep)
		}
		return nil, fmt.Errorf("two %s in a row leave the stage between them with nothing in it", StageSep)
	}
	return out, nil
}

// pipeReads names the flags that say which files a run reads. A pipeline
// reads them once, at the top: a later stage looks inside what it was handed,
// so a walk described there would be one nothing carries out.
var pipeReads = map[string]bool{
	"ext": true, "hidden": true, "no-ignore": true,
}

// pipePrints names the flags that describe the printed page, and the shapes
// that stand a tally or a file name where a result would have gone. Only the
// last stage prints, so anywhere else each of these would be read, understood,
// and then change nothing.
var pipePrints = map[string]bool{
	"format": true, "json": true, "stream": true, "outline": true,
	"color": true, "truncate": true, "separator": true, "no-breadcrumb": true,
	"n": true, "line-number": true, "N": true, "no-line-number": true,
	"c": true, "count": true, "l": true, "files-with-matches": true,
	"q": true, "quiet": true,
}

// pipeWhole names the flags that answer a question about the command rather
// than search anything. They belong to a run that has no stages at all.
var pipeWhole = map[string]bool{
	"h": true, "help": true, "V": true, "version": true,
}

// StageFlags refuses the flags a stage cannot honour where it stands: only the
// first stage names the files, only the last one prints or writes, and the
// stages in between select. It is the rule OutlineFlags and StreamFlags follow
// too -- a flag that would be read and then change nothing is refused by name
// rather than dropped in silence.
func StageFlags(fs *flag.FlagSet, i, n int) error {
	if named := Given(fs, func(name string) bool { return pipeWhole[name] }); named != "" {
		return fmt.Errorf("%s asks about the command rather than about a search, so it takes no %s", named, StageSep)
	}
	if named := Given(fs, func(name string) bool { return name == "apply" }); named != "" {
		return errors.New("--apply carries its own searches and its own edits, so it is a whole run rather than a stage of one")
	}
	if i > 0 {
		if named := Given(fs, func(name string) bool { return pipeReads[name] }); named != "" {
			return fmt.Errorf("a pipeline reads its files once, so %s belongs on the first stage", named)
		}
	}
	if i < n-1 {
		if named := Given(fs, func(name string) bool { return streamEdits[name] }); named != "" {
			return fmt.Errorf("a stage hands its nodes to the next one, so %s belongs on the last stage", named)
		}
		if named := Given(fs, func(name string) bool { return pipePrints[name] }); named != "" {
			return fmt.Errorf("only the last stage prints, so %s belongs there", named)
		}
	}
	return nil
}
