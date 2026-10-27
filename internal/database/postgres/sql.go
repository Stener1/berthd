package postgres

import (
	"strings"
	"unicode/utf8"
)

// What is read of a statement before it is sent is little: how many
// statements the string holds, the word it begins with, and its shape for
// the log. None of it is what keeps a statement from writing; the
// transaction does that. So the reading here only has to be right about
// where a string, a comment and a dollar quote begin and end.

const (
	tSpace = iota // white space and comments
	tWord         // a keyword or an identifier, quoted or not
	tValue        // a string, a dollar-quoted string, a number
	tParam        // $1
	tSemi         // ;
	tOther        // anything else, one byte at a time
)

// lex walks a statement and calls emit for each piece of it.
func lex(sql string, emit func(kind int, text string)) {
	i := 0
	for i < len(sql) {
		start := i
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			for i < len(sql) && strings.IndexByte(" \t\n\r\f\v", sql[i]) >= 0 {
				i++
			}
			emit(tSpace, sql[start:i])
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			if nl := strings.IndexByte(sql[i:], '\n'); nl >= 0 {
				i += nl
			} else {
				i = len(sql)
			}
			emit(tSpace, sql[start:i])
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			// Postgres's block comments nest.
			depth := 0
			for i < len(sql) {
				switch {
				case strings.HasPrefix(sql[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(sql[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
				if depth == 0 {
					break
				}
			}
			emit(tSpace, sql[start:i])
		case c == '\'':
			i = quoted(sql, i, '\'', false)
			emit(tValue, sql[start:i])
		case c == '"':
			i = quoted(sql, i, '"', false)
			emit(tWord, sql[start:i])
		case c == '$':
			if i+1 < len(sql) && digit(sql[i+1]) {
				i++
				for i < len(sql) && digit(sql[i]) {
					i++
				}
				emit(tParam, sql[start:i])
				break
			}
			if end, ok := dollarQuoted(sql, i); ok {
				i = end
				emit(tValue, sql[start:i])
				break
			}
			i++
			emit(tOther, sql[start:i])
		case digit(c) || (c == '.' && i+1 < len(sql) && digit(sql[i+1])):
			for i < len(sql) {
				d := sql[i]
				if digit(d) || letter(d) || d == '.' || d == '_' {
					i++
					continue
				}
				// The sign of an exponent: 1e-9.
				if (d == '+' || d == '-') && (sql[i-1] == 'e' || sql[i-1] == 'E') && i+1 < len(sql) && digit(sql[i+1]) {
					i++
					continue
				}
				break
			}
			emit(tValue, sql[start:i])
		case letter(c) || c == '_' || c >= utf8.RuneSelf:
			for i < len(sql) && (letter(sql[i]) || digit(sql[i]) || sql[i] == '_' || sql[i] == '$' || sql[i] >= utf8.RuneSelf) {
				i++
			}
			// E'…', B'…', X'…', N'…': a string with a letter before it.
			if i < len(sql) && sql[i] == '\'' && i-start == 1 && strings.IndexByte("eEbBxXnN", c) >= 0 {
				i = quoted(sql, i, '\'', c == 'e' || c == 'E')
				emit(tValue, sql[start:i])
				break
			}
			emit(tWord, sql[start:i])
		case c == ';':
			i++
			emit(tSemi, ";")
		default:
			i++
			emit(tOther, sql[start:i])
		}
	}
}

func digit(c byte) bool  { return c >= '0' && c <= '9' }
func letter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// quoted finds the end of a quoted piece that begins at i: after its
// closing quote, where a doubled quote is one inside it and, with escapes,
// so is one after a backslash. One never closed runs to the end.
func quoted(sql string, i int, q byte, escapes bool) int {
	i++
	for i < len(sql) {
		switch {
		case escapes && sql[i] == '\\':
			i += 2
		case sql[i] == q && i+1 < len(sql) && sql[i+1] == q:
			i += 2
		case sql[i] == q:
			return i + 1
		default:
			i++
		}
	}
	return len(sql)
}

// dollarQuoted finds the end of $tag$ … $tag$ that begins at i.
func dollarQuoted(sql string, i int) (end int, ok bool) {
	j := i + 1
	for j < len(sql) && (letter(sql[j]) || digit(sql[j]) || sql[j] == '_' || sql[j] >= utf8.RuneSelf) {
		j++
	}
	if j >= len(sql) || sql[j] != '$' {
		return 0, false
	}
	tag := sql[i : j+1]
	if close := strings.Index(sql[j+1:], tag); close >= 0 {
		return j + 1 + close + len(tag), true
	}
	return len(sql), true
}

// statements counts the statements in a string, and gives the word the
// first begins with, in capitals. A string of comments holds none.
func statements(sql string) (count int, lead string) {
	inOne := false
	lex(sql, func(kind int, text string) {
		switch kind {
		case tSpace:
		case tSemi:
			inOne = false
		default:
			if !inOne {
				inOne = true
				count++
			}
			if count == 1 && lead == "" && kind == tWord {
				lead = strings.ToUpper(text)
			}
		}
	})
	return count, lead
}

// shape is a statement with every value taken out: strings and numbers
// become ?, comments go, white space collapses. It is what a write is
// logged as.
func shape(sql string) string {
	var b strings.Builder
	space := false
	lex(sql, func(kind int, text string) {
		switch kind {
		case tSpace:
			space = b.Len() > 0
			return
		case tValue:
			text = "?"
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteString(text)
	})
	out := b.String()
	const most = 400
	if len(out) > most {
		out = strings.ToValidUTF8(out[:most], "") + " …"
	}
	return out
}
