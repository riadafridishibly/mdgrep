package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// StageSep divides one stage of a pipeline from the next. It is read before
// the flags are, the way a shell reads a pipe before the commands around it,
// so it separates wherever it stands rather than being a value some flag
// could have taken.
const StageSep = "--then"

// ExecFlag spells a whole pipeline in one string, so a query can be kept in a
// variable, a config file or a script rather than written out along a command
// line. The string is words and pipes, and comes to the same stages the line
// itself would have made.
const ExecFlag = "--exec"

// ExecSep divides two stages inside ExecFlag's string. The quoting there is
// mdgrep's own rather than the shell's, which is what lets a pattern hold the
// pipe character: only a bare word that is nothing but this separates.
const ExecSep = "|"

// Stages splits a command line into the stages of a pipeline. Each stage is a
// whole mdgrep command line of its own -- its own pattern, its own filters,
// its own selection -- and every stage but the first searches only inside the
// nodes the stage before it selected.
//
// A line with no separator in it is a single stage, which is every command
// mdgrep took before there were stages at all.
func Stages(args []string) ([][]string, error) {
	line, rest, given, err := execLine(args)
	if err != nil {
		return nil, err
	}
	if !given {
		return checked(split(args, StageSep), StageSep)
	}
	out, err := splitExec(line)
	if err != nil {
		return nil, err
	}
	// The pipeline is checked as it was written, before the files are added
	// to it: a path standing beside --exec names a file, and a stage missing
	// its search is not one a path can fill in for.
	if out, err = checked(out, ExecSep); err != nil {
		return nil, err
	}
	// Whatever stood beside --exec names the files, and the files belong to
	// the stage that walks them.
	out[0] = append(out[0], rest...)
	return out, nil
}

// split cuts a list of words into the stages the separator divides it into.
// The list always holds at least one stage, empty though it may be.
//
// A bare "--" ends the separators the way it ends the flags: a caller who
// wrote it to keep a filename from being read as a flag meant to keep it from
// being read as a pipeline too.
func split(args []string, sep string) [][]string {
	out := [][]string{{}}
	paths := false
	for _, a := range args {
		if a == sep && !paths {
			out = append(out, []string{})
			continue
		}
		if a == "--" {
			paths = true
		}
		out[len(out)-1] = append(out[len(out)-1], a)
	}
	return out
}

// checked refuses a pipeline with a hole in it, since a separator joins two
// searches and has to have one on each side. A line holding no separator is
// one stage and cannot have a hole, however little it says.
func checked(out [][]string, sep string) ([][]string, error) {
	if len(out) == 1 {
		return out, nil
	}
	for i, stage := range out {
		if len(stage) > 0 {
			continue
		}
		switch i {
		case 0:
			return nil, fmt.Errorf("%s narrows a search, so it wants one before it", sep)
		case len(out) - 1:
			return nil, fmt.Errorf("%s hands its nodes to another search, so it wants one after it", sep)
		}
		return nil, fmt.Errorf("two %s in a row leave the stage between them with nothing in it", sep)
	}
	return out, nil
}

// execLine finds --exec on a command line and hands back the pipeline it was
// given together with whatever stood beside it. Like the stage separator it is
// read before the flags are, since what it holds is the flags of every stage.
//
// Only paths may stand beside it: a flag outside the string is one the string
// was written to carry, and honouring it on some stage the caller did not
// name is a guess a search should not make.
func execLine(args []string) (line string, rest []string, given bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// Everything past a "--" the caller wrote is a path, dashes and
			// all, so the flag is not looked for out there.
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case a == ExecFlag:
			if i+1 >= len(args) {
				return "", nil, false, fmt.Errorf("%s wants a pipeline to run", ExecFlag)
			}
			i++
			line = args[i]
		case strings.HasPrefix(a, ExecFlag+"="):
			line = strings.TrimPrefix(a, ExecFlag+"=")
		default:
			rest = append(rest, a)
			continue
		}
		if given {
			return "", nil, false, fmt.Errorf("%s spells a whole pipeline, so it is given once", ExecFlag)
		}
		given = true
	}
	if !given {
		return "", nil, false, nil
	}
	for _, a := range rest {
		switch {
		case a == "--":
			// Everything past a "--" the caller wrote is a path, dashes and
			// all, which is exactly what may stand beside --exec.
			return line, rest, true, nil
		case a == StageSep:
			return "", nil, false, fmt.Errorf("%s spells a pipeline in one string and %s spells one along the line; use one", ExecFlag, StageSep)
		case len(a) > 1 && a[0] == '-':
			return "", nil, false, fmt.Errorf("%s holds the flags of every stage, so %s belongs inside it", ExecFlag, a)
		}
	}
	return line, rest, true, nil
}

// splitExec reads --exec's argument as a pipeline: words the way a shell reads
// them, quotes and all, and a bare word that is nothing but "|" between one
// stage and the next. The quoting is mdgrep's own here rather than the
// shell's, which is what keeps the pipe character usable in a pattern --
// "^(alpha|beta)" is one word and never a separator, and a quoted "|" is a
// word too.
func splitExec(line string) ([][]string, error) {
	words, err := execWords(line)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ExecFlag, err)
	}
	out := [][]string{{}}
	for _, w := range words {
		switch {
		case w.bare && (w.text == ExecSep || w.text == StageSep):
			out = append(out, []string{})
		case w.bare && (w.text == ExecFlag || strings.HasPrefix(w.text, ExecFlag+"=")):
			return nil, fmt.Errorf("%s is read before the flags are, so it cannot stand inside one", ExecFlag)
		default:
			out[len(out)-1] = append(out[len(out)-1], w.text)
		}
	}
	return out, nil
}

// lineBreak is the length of the line break a string opens with, and zero
// when it opens with anything else.
func lineBreak(s string) int {
	switch {
	case strings.HasPrefix(s, "\r\n"):
		return 2
	case strings.HasPrefix(s, "\n"), strings.HasPrefix(s, "\r"):
		return 1
	}
	return 0
}

// isSpace reports whether a byte divides two words. It is the ASCII
// whitespace and no more: a space a caller went to the trouble of writing in
// some other alphabet is one they meant to search for.
func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// word is one argument read out of --exec's string. Only a word written
// without any quoting can be the separator, so that a caller who means the
// pipe character itself has a way to say so.
type word struct {
	text string
	bare bool
}

// execWords splits a string into words the way a shell would: ASCII
// whitespace divides them, single quotes are literal throughout, double quotes are
// literal but for a backslash before a quote or a backslash, and a backslash
// outside both stands for the character after it. Quoted emptiness is a word,
// so "" is how a stage asks for the pattern that matches everything.
func execWords(line string) ([]word, error) {
	var out []word
	var sb strings.Builder
	started, bare := false, true
	flush := func() {
		if !started {
			return
		}
		out = append(out, word{text: sb.String(), bare: bare})
		sb.Reset()
		started, bare = false, true
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case isSpace(c):
			flush()
		case c == '\'':
			started, bare = true, false
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("a quote opened at position %d is never closed", i+1)
			}
			sb.WriteString(line[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			started, bare = true, false
			open := i
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\') {
					i++
				}
				sb.WriteByte(line[i])
			}
			if i >= len(line) {
				return nil, fmt.Errorf("a quote opened at position %d is never closed", open+1)
			}
		case c == '\\' && i+1 < len(line):
			// A backslash before a line break is a continuation, the way it is
			// in a shell: it joins what stands on either side of it and is
			// itself nothing at all.
			if n := lineBreak(line[i+1:]); n > 0 {
				i += n
				continue
			}
			started, bare = true, false
			i++
			sb.WriteByte(line[i])
		default:
			started = true
			sb.WriteByte(c)
		}
	}
	flush()
	return out, nil
}

// pipeReads names the flags that say which files a run reads. A pipeline
// reads them once, at the top: a later stage looks inside what it was handed,
// so a walk described there would be one nothing carries out.
var pipeReads = map[string]bool{
	"ext": true, "hidden": true, "no-ignore": true,
}

// pipeFormats names the flags that choose the shape of the printed page.
var pipeFormats = map[string]bool{
	"format": true, "json": true, "stream": true, "outline": true,
}

// pipePrints reports whether a flag describes the printed page rather than the
// search: the format itself, and everything a stream already refuses -- the
// decoration, and the shapes that stand a tally or a file name where a result
// would have gone. Only the last stage prints, so anywhere else each of these
// would be read, understood, and then change nothing. The two lists are one
// list so that a new output flag cannot be honoured on the wrong stage by
// being added to only one of them.
func pipePrints(name string) bool {
	return pipeFormats[name] || streamIgnores[name]
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
		// An address names lines of one file, and it is the first stage that
		// says which files a run reads. Further along it would be applied to
		// whatever the stage before it handed over, in however many files --
		// and its two refusals, that only one file could answer and that the
		// lines are there, are both asked where the files are named.
		if named := Given(fs, func(name string) bool { return name == "at" }); named != "" {
			return fmt.Errorf("%s names lines of one file, so it belongs on the stage that names the files", named)
		}
	}
	if i < n-1 {
		if named := Given(fs, func(name string) bool { return streamEdits[name] }); named != "" {
			return fmt.Errorf("a stage hands its nodes to the next one, so %s belongs on the last stage", named)
		}
		if named := Given(fs, pipePrints); named != "" {
			return fmt.Errorf("only the last stage prints, so %s belongs there", named)
		}
	}
	return nil
}
