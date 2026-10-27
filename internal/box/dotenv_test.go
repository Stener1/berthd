package box

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// values is what a file's text sets, each reference filled in from the file
// itself; a variable that is not one to use has its reason in why.
func dotenvValues(t *testing.T, text string) (values, why map[string]string) {
	t.Helper()
	set := &dotenvSet{vars: map[string]dotenvVar{}}
	for _, e := range parseDotenv([]byte(text)) {
		set.vars[e.name] = dotenvVar{parts: e.parts, file: ".env"}
	}
	values, why = map[string]string{}, map[string]string{}
	for _, name := range set.names() {
		if v, reason := set.value(name, nil); reason != "" {
			why[name] = reason
		} else {
			values[name] = v
		}
	}
	return values, why
}

func TestDotenvSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       map[string]string
		why        map[string]string
	}{
		{"plain", "DATABASE_URL=postgres://app:hunter2@127.0.0.1:5432/acme\nPORT=3000\n",
			map[string]string{"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:5432/acme", "PORT": "3000"}, nil},
		{"no newline at the end", "A=1", map[string]string{"A": "1"}, nil},
		{"comments and empty lines", "# a comment\n\n  # another\nA=1 # after a value\nB=one#two\nC=#nothing\nD=a # b # c\n",
			map[string]string{"A": "1", "B": "one#two", "D": "a"}, map[string]string{"C": dotenvEmpty}},
		{"export", "export A=1\nexport   B='two'\nexportC=3\nexport=4\n",
			map[string]string{"A": "1", "B": "two", "exportC": "3", "export": "4"}, nil},
		{"white space round the sign and the value", "A = 1\n  B=  two words  \nC\t=\t3\t\n",
			map[string]string{"A": "1", "B": "two words", "C": "3"}, nil},
		{"single quotes are literal", `A='it is $HOME and ${NOT} a \n reference' # said so` + "\n",
			map[string]string{"A": `it is $HOME and ${NOT} a \n reference`}, nil},
		{"double quotes read escapes", `A="two\nlines\tand a \"quote\" and a \\ and \$5 and \x"` + "\n",
			map[string]string{"A": "two\nlines\tand a \"quote\" and a \\ and $5 and \\x"}, nil},
		{"backticks are literal", "A=`a 'single' and a \"double\" $X`\n", map[string]string{"A": `a 'single' and a "double" $X`}, nil},
		{"a quoted value over several lines", "KEY=\"-----BEGIN-----\nabc\ndef\n-----END-----\"\nAFTER=1\nS='one\ntwo'\n",
			map[string]string{"KEY": "-----BEGIN-----\nabc\ndef\n-----END-----", "AFTER": "1", "S": "one\ntwo"}, nil},
		{"a # inside quotes is no comment", `A="a # b"` + "\nB='c # d'\n", map[string]string{"A": "a # b", "B": "c # d"}, nil},
		{"a quote that is never closed is a character", "A=\"half\nB=2\nC='it\n", map[string]string{"A": `"half`, "B": "2", "C": "'it"}, nil},
		{"an empty value", "A=\nB=\"\"\nC=''\nD=1\n", map[string]string{"D": "1"}, map[string]string{"A": dotenvEmpty, "B": dotenvEmpty, "C": dotenvEmpty}},
		{"CRLF", "A=1\r\nB=\"two\"\r\n# c\r\nC='three'  \r\nD=\"over\r\nlines\"\r\n",
			map[string]string{"A": "1", "B": "two", "C": "three", "D": "over\nlines"}, nil},
		{"a byte order mark", "\xef\xbb\xbfA=1\nB=2\n", map[string]string{"A": "1", "B": "2"}, nil},
		{"a name written twice: the last wins", "A=1\nB=2\nA=3\n", map[string]string{"A": "3", "B": "2"}, nil},
		{"lines that are no variable are passed over", "just words\n=novalue\n1A=1\nA-B=2\nKEY: yaml\nA.B=3\n$(rm -rf x)\nGOOD=1\n",
			map[string]string{"GOOD": "1"}, nil},
		{"nothing is run", "A=$(whoami)\nB=`whoami`\nC=\"$(cat /etc/passwd)\"\n",
			map[string]string{"A": "$(whoami)", "B": "whoami", "C": "$(cat /etc/passwd)"}, nil},
		{"references to the file's own variables", "DB_USER=app\nDB_PASS='hunter2'\nDB_NAME=acme\n" +
			"DATABASE_URL=\"postgres://${DB_USER}:${DB_PASS}@localhost:5432/$DB_NAME?schema=public\"\nPLAIN=postgres://$DB_USER@localhost/${DB_NAME}\n",
			map[string]string{"DB_USER": "app", "DB_PASS": "hunter2", "DB_NAME": "acme",
				"DATABASE_URL": "postgres://app:hunter2@localhost:5432/acme?schema=public", "PLAIN": "postgres://app@localhost/acme"}, nil},
		{"a reference to a variable written later", "URL=${HOST}:5432\nHOST=localhost\n", map[string]string{"URL": "localhost:5432", "HOST": "localhost"}, nil},
		{"a fallback", "A=${NONE:-fallback}\nB=${NONE-other}\nEMPTY=\nC=${EMPTY:-was empty}\nD=x${EMPTY-not used}y\nSET=1\nE=${SET:-no}\n",
			map[string]string{"A": "fallback", "B": "other", "C": "was empty", "D": "xy", "SET": "1", "E": "1"}, map[string]string{"EMPTY": dotenvEmpty}},
		{"a ${NAME} nothing defines leaves the variable unresolved", "A=postgres://${PGUSER}@localhost/acme\nB=fine\n",
			map[string]string{"B": "fine"}, map[string]string{"A": dotenvUnresolved}},
		{"a bare $NAME nothing defines is a dollar sign and a word", "A=postgres://app:pa$word@localhost/acme\nB=$5\nC=a$\nD=${}\nE=${not closed\nF=${A B}\n",
			map[string]string{"A": "postgres://app:pa$word@localhost/acme", "B": "$5", "C": "a$", "D": "${}", "E": "${not closed", "F": "${A B}"}, nil},
		{"an escaped dollar sign is one", `A="cost \$HOME"` + "\nB=cost\\$HOME\nHOME=/x\n", map[string]string{"A": "cost $HOME", "B": "cost$HOME", "HOME": "/x"}, nil},
		{"a circle is unresolved, and does not hang", "A=${B}\nB=${C}\nC=${A}\nD=$D\nE=ok\n",
			map[string]string{"E": "ok"}, map[string]string{"A": dotenvUnresolved, "B": dotenvUnresolved, "C": dotenvUnresolved, "D": dotenvUnresolved}},
		{"a secret's reference is no value", "A=op://dev/shop-db/password\nB=env://PGPASSWORD\nC=\"postgres://app:${A}@localhost/acme\"\nD=opaque\n",
			map[string]string{"D": "opaque"}, map[string]string{"A": dotenvSecret, "B": dotenvSecret, "C": dotenvSecret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := dotenvValues(t, tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("values = %q\n         want %q", got, tc.want)
			}
			if tc.why == nil {
				tc.why = map[string]string{}
			}
			if !reflect.DeepEqual(why, tc.why) {
				t.Errorf("not used = %q, want %q", why, tc.why)
			}
		})
	}
}

func TestADotenvValueIsHeldToALength(t *testing.T) {
	long := strings.Repeat("x", maxAttachValue+1)
	_, why := dotenvValues(t, "A="+long+"\nB=ok\n")
	if why["A"] != dotenvTooLong || len(why) != 1 {
		t.Fatalf("a value past the limit: %v", why)
	}
	// One that only grows past it once its references are filled in, too:
	// sixteen of a kilobyte each are short, and two thousand of them are not.
	text := "K=" + strings.Repeat("k", 1024) + "\nA=" + strings.Repeat("${K}", 40) + "\nB=" + strings.Repeat("${A}", 40) + "\n"
	values, why := dotenvValues(t, text)
	if why["A"] != dotenvTooLong || why["B"] != dotenvTooLong || len(values["K"]) != 1024 {
		t.Fatalf("a value that grows past the limit: %v", why)
	}
}

// A reference is looked for in the service's own environment before the
// files, and a secret there is not read.
func TestADotenvReferenceIsFilledInFromTheServicesEnvironmentFirst(t *testing.T) {
	set := &dotenvSet{vars: map[string]dotenvVar{}}
	for _, e := range parseDotenv([]byte("DB_NAME=from_the_file\nDATABASE_URL=postgres://localhost/${DB_NAME}\nWITH_SECRET=postgres://app:${DB_PASSWORD}@localhost/acme\n")) {
		set.vars[e.name] = dotenvVar{parts: e.parts}
	}
	outer := func(name string) (string, bool, bool) {
		switch name {
		case "DB_NAME":
			return "acme_fix_orders", true, true
		case "DB_PASSWORD":
			return "", true, false
		}
		return "", false, false
	}
	if v, why := set.value("DATABASE_URL", outer); v != "postgres://localhost/acme_fix_orders" || why != "" {
		t.Errorf("with the service's DB_NAME: %q (%s)", v, why)
	}
	if v, why := set.value("DATABASE_URL", nil); v != "postgres://localhost/from_the_file" || why != "" {
		t.Errorf("with the file's DB_NAME: %q (%s)", v, why)
	}
	if v, why := set.value("WITH_SECRET", outer); v != "" || why != dotenvSecret {
		t.Errorf("a reference to a secret: %q (%s)", v, why)
	}
}

func TestDotenvFilesAreReadInOrderFromTheWorktreeAlone(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt, elsewhere := filepath.Join(root, "worktree"), filepath.Join(root, "elsewhere")
	for _, dir := range []string{wt, elsewhere, filepath.Join(wt, "config"), filepath.Join(wt, ".env.development.local")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing there: nothing read, nothing skipped.
	if set := readDotenv(filepath.Join(root, "worktree-that-is-not-there")); len(set.files)+len(set.skipped)+len(set.vars) != 0 {
		t.Fatalf("a folder that is not there: %+v", set)
	}
	if set := readDotenv(elsewhere); len(set.files)+len(set.skipped)+len(set.vars) != 0 {
		t.Fatalf("a folder with no dotenv file: %+v", set)
	}

	write(filepath.Join(wt, ".env"), "A=env\nB=env\nC=env\nONLY_ENV=1\nNAME=acme\n")
	// A link to a file inside the worktree is the worktree's own.
	write(filepath.Join(wt, "config", "development.env"), "B=development\nC=development\nURL=postgres://localhost/${NAME}\n")
	if err := os.Symlink(filepath.Join("config", "development.env"), filepath.Join(wt, ".env.development")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(wt, ".env.local"), "C=local\nNAME=acme_local\n")
	// Other files beside them are not read, whatever they are called.
	write(filepath.Join(wt, ".env.production"), "A=production\nD=production\n")
	write(filepath.Join(wt, ".env.test.local"), "A=test\n")
	write(filepath.Join(wt, ".envrc"), "export A=envrc\n")
	write(filepath.Join(wt, "config", ".env"), "A=nested\n")
	set := readDotenv(wt)
	if !reflect.DeepEqual(set.files, []string{".env", ".env.development", ".env.local"}) {
		t.Errorf("files read = %v", set.files)
	}
	// A folder by a dotenv file's name is not one.
	if !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.development.local", dotenvNotAFile}}) {
		t.Errorf("skipped = %v", set.skipped)
	}
	for name, want := range map[string][2]string{
		"A": {"env", ".env"}, "B": {"development", ".env.development"}, "C": {"local", ".env.local"},
		"ONLY_ENV": {"1", ".env"},
		// A reference is to the last value of any of the files.
		"URL": {"postgres://localhost/acme_local", ".env.development"},
	} {
		if v, why := set.value(name, nil); v != want[0] || why != "" || set.file(name) != want[1] {
			t.Errorf("%s = %q from %s (%s), want %q from %s", name, v, set.file(name), why, want[0], want[1])
		}
	}
	if _, ok := set.vars["D"]; ok {
		t.Error(".env.production was read")
	}

	// A link that leads out of the worktree is not followed: to a file
	// beside it, to one by an absolute path, and through a linked folder.
	os.Remove(filepath.Join(wt, ".env.development.local"))
	write(filepath.Join(elsewhere, "secrets.env"), "A=from_outside\nSTOLEN=1\n")
	for what, target := range map[string]string{
		"a relative link out":    filepath.Join("..", "elsewhere", "secrets.env"),
		"an absolute link out":   filepath.Join(elsewhere, "secrets.env"),
		"a link through a link":  filepath.Join("way-out", "secrets.env"),
		"a link to /etc/passwd":  "/etc/passwd",
		"a link to the worktree": ".",
	} {
		os.Remove(filepath.Join(wt, "way-out"))
		if err := os.Symlink(elsewhere, filepath.Join(wt, "way-out")); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(wt, ".env.local")
		os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		set := readDotenv(wt)
		wantWhy := dotenvOutside
		if target == "." {
			wantWhy = dotenvNotAFile
		}
		if !reflect.DeepEqual(set.files, []string{".env", ".env.development"}) || !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.local", wantWhy}}) {
			t.Errorf("%s: read %v, skipped %v", what, set.files, set.skipped)
		}
		if v, _ := set.value("A", nil); v != "env" {
			t.Errorf("%s: A = %q", what, v)
		}
		if _, ok := set.vars["STOLEN"]; ok {
			t.Errorf("%s: the file outside the worktree was read", what)
		}
	}
	// A link to nothing is not a file either.
	os.Remove(filepath.Join(wt, ".env.local"))
	if err := os.Symlink("not-there", filepath.Join(wt, ".env.local")); err != nil {
		t.Fatal(err)
	}
	if set := readDotenv(wt); !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.local", dotenvNoReading}}) {
		t.Errorf("a link to nothing: skipped %v", set.skipped)
	}
	os.Remove(filepath.Join(wt, ".env.local"))

	// A file past the size that is read is not read at all, and what an
	// earlier file set stands.
	write(filepath.Join(wt, ".env.local"), "A=local\n"+strings.Repeat("# padding\n", maxDotenvFile/10+1))
	set = readDotenv(wt)
	if !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.local", dotenvTooLarge}}) {
		t.Errorf("an oversized file: skipped %v", set.skipped)
	}
	if v, _ := set.value("A", nil); v != "env" {
		t.Errorf("an oversized file was read in part: A = %q", v)
	}
	// Exactly at the size, it is.
	exact := "A=local\n"
	exact += strings.Repeat("#", maxDotenvFile-len(exact))
	write(filepath.Join(wt, ".env.local"), exact)
	if v, _ := readDotenv(wt).value("A", nil); v != "local" {
		t.Errorf("a file of exactly the size: A = %q", v)
	}
	// Not text.
	write(filepath.Join(wt, ".env.local"), "A=bin\x00ary\n")
	if set := readDotenv(wt); !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.local", dotenvNotText}}) {
		t.Errorf("a file with a NUL: skipped %v", set.skipped)
	}
	// A pipe is not waited on.
	os.Remove(filepath.Join(wt, ".env.local"))
	if err := syscall.Mkfifo(filepath.Join(wt, ".env.local"), 0o600); err != nil {
		t.Fatal(err)
	}
	if set := readDotenv(wt); !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.local", dotenvNotAFile}}) {
		t.Errorf("a pipe: skipped %v", set.skipped)
	}
	os.Remove(filepath.Join(wt, ".env.local"))
	// One this user cannot read.
	if os.Getuid() != 0 {
		write(filepath.Join(wt, ".env.local"), "A=local\n")
		if err := os.Chmod(filepath.Join(wt, ".env.local"), 0); err != nil {
			t.Fatal(err)
		}
		if set := readDotenv(wt); !reflect.DeepEqual(set.skipped, []dotenvSkipped{{".env.local", dotenvNoReading}}) {
			t.Errorf("a file that cannot be read: skipped %v", set.skipped)
		}
	}
}
