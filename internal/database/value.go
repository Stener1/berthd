package database

import (
	"math"
	"strconv"
	"unicode/utf8"
)

// How a cell becomes JSON is the same for every engine:
//
//	null                          null
//	a boolean                     true, false
//	an integer that fits a double a number
//	a float                       a number (NaN and the infinities: a string)
//	anything else                 a string, as the database prints it
//
// "Anything else" is where an engine has work to do: a big integer and a
// numeric keep every digit, a timestamp is ISO 8601, bytes are \x and hex.
// Int, Float and Grid are the parts of that every engine shares.

const (
	// MaxCell is the most characters of a cell that are sent. A longer
	// one is cut there, and listed in the answer's "cut".
	MaxCell = 4000
	// MaxBytes is about how much one answer's rows may hold. Past it the
	// rows stop, and the answer says there were more.
	MaxBytes = 8 << 20
	// exact is the largest integer a double holds exactly: 2^53.
	exact = 1 << 53
)

// Int is an integer's cell from its digits: a number when a double holds it
// exactly, else the digits.
func Int(text string) any {
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n > exact || n < -exact {
		return text
	}
	return n
}

// Float is a float's cell from its text: a number, or the text for the
// values JSON has no number for.
func Float(text string) any {
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return text
	}
	return f
}

// Grid collects an answer's rows, cuts the cells that are too long and
// counts what it holds.
type Grid struct {
	Rows [][]any
	Cut  [][2]int
	// Max is how many bytes it takes before it is full; zero is MaxBytes.
	Max  int
	size int
}

// NewGrid makes an empty grid: its lists are never null.
func NewGrid() *Grid { return &Grid{Rows: [][]any{}, Cut: [][2]int{}} }

// Add takes a row, and reports whether there is room for another.
func (g *Grid) Add(row []any) bool {
	for i, cell := range row {
		s, ok := cell.(string)
		if !ok {
			g.size += 8
			continue
		}
		if short, cut := CutCell(s); cut {
			row[i], s = short, short
			g.Cut = append(g.Cut, [2]int{len(g.Rows), i})
		}
		g.size += len(s) + 2
	}
	g.Rows = append(g.Rows, row)
	limit := g.Max
	if limit == 0 {
		limit = MaxBytes
	}
	return g.size < limit
}

// CutCell shortens a cell to MaxCell characters, and says whether it did.
func CutCell(s string) (string, bool) {
	// Bytes are never fewer than characters.
	if len(s) <= MaxCell {
		return s, false
	}
	n := 0
	for i := range s {
		if n == MaxCell {
			return s[:i], true
		}
		n++
	}
	return s, false
}

// Runes is how many characters a cell has, for an engine that cuts in the
// database and needs to know what it asked for.
func Runes(s string) int { return utf8.RuneCountInString(s) }
