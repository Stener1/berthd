package box

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
)

// A worktree's dotenv files. Many projects keep their development settings
// there and nowhere Shipyard assembles an environment from: the dev server
// loads .env itself. So that a plugin's helper can still be told of a
// worktree's database (helperenv.go), berthd reads them, and only them:
//
//	.env  .env.development  .env.local  .env.development.local
//
// in the worktree's own folder, in that order, a later file's value for a
// variable replacing an earlier one's. That is the order the usual loaders
// give a development server (Next.js, Vite, dotenv-flow).
//
// What is read is held to what a helper asked for: the caller keeps the
// variables a helper's manifest names and drops the rest. Nothing of a file
// is logged, and no error repeats a line of one.
//
// The syntax, which is dotenv's and not a shell's:
//
//	# a comment                     a line that starts with #
//	NAME=value                      to the end of the line, trimmed; " #" starts a comment
//	export NAME=value               "export " is ignored
//	NAME='as $it is'                single quotes: every character as written
//	NAME="two\nlines, a \"quote\""  double quotes: \n \r \t \\ \" \$ are escapes
//	NAME=`as it is`                 backticks: as single quotes
//
// A quoted value may run over several lines. A line that is none of these
// is passed over. A file may start with a byte order mark, and its lines may
// end in CRLF.
//
// Outside single quotes and backticks a value may refer to another
// variable: ${NAME}, ${NAME:-fallback}, ${NAME-fallback} or $NAME. A
// reference is filled in from the variables of these same files (whichever
// file, the last value winning), and, when a service is being started, from
// the environment Shipyard gives that service first, as a loader would find
// it there. It is never filled in from berthd's own environment, and
// nothing is ever run: there is no $(command). A ${NAME} that nothing
// defines, or that goes round in a circle, leaves the variable unresolved:
// it is not used, rather than used with a hole in it. A bare $NAME that
// nothing defines stays as it was written, since it is as likely a dollar
// sign in a password.

// dotenvFiles are the files read, in the order they are read.
var dotenvFiles = []string{".env", ".env.development", ".env.local", ".env.development.local"}

const (
	// maxDotenvFile is the most of one file that is read. A larger file is
	// not read at all: half a file is not what the project wrote.
	maxDotenvFile = 256 << 10
	// maxDotenvDepth is how many references deep a value is followed.
	maxDotenvDepth = 16
)

// Why a dotenv file that is there was not read.
const (
	dotenvOutside   = "outside"    // a link to somewhere outside the worktree
	dotenvNotAFile  = "not_a_file" // a folder, a pipe, a device
	dotenvTooLarge  = "too_large"  // more than maxDotenvFile
	dotenvNotText   = "not_text"   // it holds a NUL byte
	dotenvNoReading = "unreadable" // it could not be opened or read
)

// Why a variable a helper asked for was not given to it.
const (
	dotenvSecret     = "secret"     // it names a secret (op://, env://), and holds none
	dotenvUnresolved = "unresolved" // it refers to a variable nothing defines
	dotenvTooLong    = "too_long"   // longer than a helper is told (maxAttachValue)
	dotenvEmpty      = "empty"      // it is set to nothing
)

// dotenvPart is a piece of a value: text as it stands, or a reference.
type dotenvPart struct {
	text string
	// ref is the variable referred to; "" for text.
	ref string
	// braced: ${NAME}, as against $NAME. alt is its fallback, when hasAlt;
	// orEmpty: the fallback also stands in for an empty value (":-").
	braced, hasAlt, orEmpty bool
	alt                     string
}

type dotenvVar struct {
	parts []dotenvPart
	// file is the file whose value this is.
	file string
}

// dotenvSkipped is a file that is there and was not read.
type dotenvSkipped struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// dotenvSet is what a worktree's dotenv files say.
type dotenvSet struct {
	// files are the ones that were read, in the order they were read.
	files   []string
	skipped []dotenvSkipped
	vars    map[string]dotenvVar
}

// names are the variables the files set, in no order.
func (s *dotenvSet) names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.vars))
	for name := range s.vars {
		out = append(out, name)
	}
	return out
}

// file is the file a variable's value came from.
func (s *dotenvSet) file(name string) string { return s.vars[name].file }

// value is a variable's value with its references filled in, or why it is
// not one to use. outer, when given, is looked in before the files for a
// reference: found says the variable is there, and usable that its value
// may be read (a secret's is not).
func (s *dotenvSet) value(name string, outer func(string) (value string, found, usable bool)) (string, string) {
	v, why := s.resolve(name, outer, map[string]bool{}, 0)
	switch {
	case why != "":
		return "", why
	case v == "":
		return "", dotenvEmpty
	case IsSecretRef(v):
		return "", dotenvSecret
	case len(v) > maxAttachValue:
		return "", dotenvTooLong
	}
	return v, ""
}

func (s *dotenvSet) resolve(name string, outer func(string) (string, bool, bool), seen map[string]bool, depth int) (string, string) {
	def, ok := s.vars[name]
	if !ok || seen[name] || depth > maxDotenvDepth {
		return "", dotenvUnresolved
	}
	seen[name] = true
	defer delete(seen, name)
	var b strings.Builder
	for _, p := range def.parts {
		if p.ref == "" {
			b.WriteString(p.text)
		} else {
			v, found, why := s.lookup(p.ref, outer, seen, depth)
			switch {
			case why != "":
				return "", why
			case p.hasAlt && (!found || p.orEmpty && v == ""):
				b.WriteString(p.alt)
			case found:
				b.WriteString(v)
			case p.braced:
				return "", dotenvUnresolved
			default:
				// $NAME with no such variable: a dollar sign and a word.
				b.WriteString("$" + p.ref)
			}
		}
		if b.Len() > maxAttachValue {
			return "", dotenvTooLong
		}
	}
	return b.String(), ""
}

// lookup is the value a reference stands for, and whether there is one.
func (s *dotenvSet) lookup(name string, outer func(string) (string, bool, bool), seen map[string]bool, depth int) (value string, found bool, why string) {
	if outer != nil {
		if v, there, usable := outer(name); there {
			if !usable {
				return "", false, dotenvSecret
			}
			return v, true, ""
		}
	}
	if _, ok := s.vars[name]; !ok {
		return "", false, ""
	}
	v, why := s.resolve(name, outer, seen, depth+1)
	if why != "" {
		return "", false, why
	}
	// A reference to a secret's name would put the name where the secret
	// belongs.
	if IsSecretRef(v) {
		return "", false, dotenvSecret
	}
	return v, true, ""
}

// dotenvReads counts the times a worktree's dotenv files were looked for,
// so a test can hold berthd to not looking when nobody is there to be told.
var dotenvReads atomic.Int64

// readDotenv reads a worktree's dotenv files. It never fails: a file that
// is not there is not there, and one that cannot be used is in skipped.
func readDotenv(dir string) *dotenvSet {
	dotenvReads.Add(1)
	set := &dotenvSet{files: []string{}, skipped: []dotenvSkipped{}, vars: map[string]dotenvVar{}}
	// Every file is opened through the worktree's own folder, which refuses
	// a link that leads out of it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return set
	}
	defer root.Close()
	for _, name := range dotenvFiles {
		raw, why := readDotenvFile(root, name)
		if why != "" {
			set.skipped = append(set.skipped, dotenvSkipped{File: name, Reason: why})
			continue
		}
		if raw == nil {
			continue
		}
		set.files = append(set.files, name)
		for _, e := range parseDotenv(raw) {
			set.vars[e.name] = dotenvVar{parts: e.parts, file: name}
		}
	}
	return set
}

// readDotenvFile reads one file of a worktree's folder: nil when it is not
// there, and why when it is there and is not read.
func readDotenvFile(root *os.Root, name string) (raw []byte, why string) {
	link, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, dotenvNoReading
	}
	linked := link.Mode()&os.ModeSymlink != 0
	// What it is, a link followed: inside the worktree, or not at all.
	info, err := root.Stat(name)
	if err != nil {
		if linked && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
			return nil, dotenvOutside
		}
		return nil, dotenvNoReading
	}
	if !info.Mode().IsRegular() {
		return nil, dotenvNotAFile
	}
	if info.Size() > maxDotenvFile {
		return nil, dotenvTooLarge
	}
	// Not waited for, should it have become a pipe since it was looked at.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if linked && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
			return nil, dotenvOutside
		}
		return nil, dotenvNoReading
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, dotenvNotAFile
	}
	raw, err = io.ReadAll(io.LimitReader(f, maxDotenvFile+1))
	switch {
	case err != nil:
		return nil, dotenvNoReading
	case len(raw) > maxDotenvFile:
		return nil, dotenvTooLarge
	case bytes.IndexByte(raw, 0) >= 0:
		return nil, dotenvNotText
	}
	if raw == nil {
		raw = []byte{}
	}
	return raw, ""
}

type dotenvEntry struct {
	name  string
	parts []dotenvPart
}

// parseDotenv reads one file's text into its variables, in the order they
// are written: a name written twice is there twice, and the last one wins.
func parseDotenv(raw []byte) []dotenvEntry {
	// A byte order mark, which some editors put first.
	src := string(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	var out []dotenvEntry
	for i := 0; i < len(src); {
		// The start of a line: the white space before it, and empty lines.
		for i < len(src) && (isBlank(src[i]) || src[i] == '\n' || src[i] == '\r') {
			i++
		}
		if i >= len(src) {
			break
		}
		lineEnd := func() int {
			if n := strings.IndexByte(src[i:], '\n'); n >= 0 {
				return i + n
			}
			return len(src)
		}
		if src[i] == '#' {
			i = lineEnd()
			continue
		}
		if rest, ok := strings.CutPrefix(src[i:], "export"); ok && rest != "" && isBlank(rest[0]) {
			i += len("export")
			for i < len(src) && isBlank(src[i]) {
				i++
			}
		}
		start := i
		for i < len(src) && isNameByte(src[i], i == start) {
			i++
		}
		name := src[start:i]
		for i < len(src) && isBlank(src[i]) {
			i++
		}
		if name == "" || i >= len(src) || src[i] != '=' {
			// Not a variable: the line is passed over.
			i = lineEnd()
			continue
		}
		i++
		for i < len(src) && isBlank(src[i]) {
			i++
		}
		if i < len(src) && (src[i] == '\'' || src[i] == '"' || src[i] == '`') {
			if value, after, ok := quotedDotenv(src, i); ok {
				parts := []dotenvPart{{text: value}}
				if src[i] == '"' {
					parts = dotenvRefs(value, true)
				}
				out = append(out, dotenvEntry{name, parts})
				// What follows the closing quote on its line is a comment.
				i = after
				i = lineEnd()
				continue
			}
			// A quote that is never closed is a character of the value.
		}
		end := lineEnd()
		value := strings.TrimSuffix(src[i:end], "\r")
		// A comment after the value starts with white space and a #.
		for at := 0; at < len(value); at++ {
			if value[at] == '#' && (at == 0 || isBlank(value[at-1])) {
				value = value[:at]
				break
			}
		}
		out = append(out, dotenvEntry{name, dotenvRefs(strings.TrimRight(value, " \t"), false)})
		i = end
	}
	return out
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

func isNameByte(c byte, first bool) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || !first && c >= '0' && c <= '9'
}

// quotedDotenv reads a quoted value that starts at src[at]: what is between
// the quotes, and where the text after the closing quote starts. In double
// quotes an escape is left for dotenvRefs to read; a backslash keeps the
// quote after it from closing the value.
func quotedDotenv(src string, at int) (value string, after int, ok bool) {
	quote := src[at]
	for i := at + 1; i < len(src); i++ {
		switch {
		case src[i] == '\\' && quote == '"' && i+1 < len(src):
			i++
		case src[i] == quote:
			return strings.ReplaceAll(src[at+1:i], "\r\n", "\n"), i + 1, true
		}
	}
	return "", 0, false
}

// dotenvRefs reads a value's references, and, in double quotes, its
// escapes. A "\$" is a dollar sign in either.
func dotenvRefs(value string, escapes bool) []dotenvPart {
	var parts []dotenvPart
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			parts = append(parts, dotenvPart{text: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\\' && i+1 < len(value) {
			next := value[i+1]
			if next == '$' {
				text.WriteByte('$')
				i++
				continue
			}
			if to, ok := dotenvEscape(next); ok && escapes {
				text.WriteByte(to)
				i++
				continue
			}
		}
		if c != '$' || i+1 >= len(value) {
			text.WriteByte(c)
			continue
		}
		if value[i+1] == '{' {
			end := strings.IndexByte(value[i:], '}')
			if end < 0 {
				text.WriteByte(c)
				continue
			}
			inside := value[i+2 : i+end]
			n := 0
			for n < len(inside) && isNameByte(inside[n], n == 0) {
				n++
			}
			p := dotenvPart{ref: inside[:n], braced: true}
			switch rest := inside[n:]; {
			case n == 0:
				p.ref = ""
			case rest == "":
			case strings.HasPrefix(rest, ":-"):
				p.hasAlt, p.orEmpty, p.alt = true, true, rest[2:]
			case strings.HasPrefix(rest, "-"):
				p.hasAlt, p.alt = true, rest[1:]
			default:
				p.ref = ""
			}
			if p.ref == "" {
				// Not a reference this reads: the characters themselves.
				text.WriteByte(c)
				continue
			}
			flush()
			parts = append(parts, p)
			i += end
			continue
		}
		n := i + 1
		for n < len(value) && isNameByte(value[n], n == i+1) {
			n++
		}
		if n == i+1 {
			text.WriteByte(c)
			continue
		}
		flush()
		parts = append(parts, dotenvPart{ref: value[i+1 : n]})
		i = n - 1
	}
	flush()
	return parts
}

// dotenvEscape is what a backslash and c stand for in double quotes.
func dotenvEscape(c byte) (byte, bool) {
	switch c {
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case '\\', '"':
		return c, true
	}
	return 0, false
}
