package groups

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func fake(db []string, proc []int) Source {
	names := map[string]string{"1000": "dev", "998": "docker", "27": "sudo", "1500": "libvirt"}
	return Source{
		Database: func() (string, []string, error) { return "1000", db, nil },
		Process:  func() ([]int, error) { return proc, nil },
		Name: func(gid string) (string, error) {
			if n, ok := names[gid]; ok {
				return n, nil
			}
			return "", errors.New("no such group")
		},
		SG:   func() string { return "/usr/bin/sg" },
		GOOS: "linux",
		UID:  1000,
	}
}

func TestFind(t *testing.T) {
	// berthd started before the docker step added dev to docker.
	m := fake([]string{"1000", "27", "998"}, []int{1000, 27}).Find()
	if !reflect.DeepEqual(m.Groups, []string{"docker"}) || m.Primary != "dev" || m.SG != "/usr/bin/sg" {
		t.Fatalf("%+v", m)
	}
	for name, s := range map[string]Source{
		"nothing new": fake([]string{"1000", "27", "998"}, []int{1000, 27, 998}),
		"not linux":   func() Source { s := fake([]string{"1000", "998"}, []int{1000}); s.GOOS = "darwin"; return s }(),
		"root":        func() Source { s := fake([]string{"1000", "998"}, []int{1000}); s.UID = 0; return s }(),
		"no sg": func() Source {
			s := fake([]string{"1000", "998"}, []int{1000})
			s.SG = func() string { return "" }
			return s
		}(),
		"unknown group": fake([]string{"1000", "4242"}, []int{1000}),
	} {
		if m := s.Find(); len(m.Groups) != 0 || len(m.Wrap([]string{"x"})) != 1 {
			t.Errorf("%s: %+v", name, m)
		}
	}
}

func TestWrap(t *testing.T) {
	m := Missing{Groups: []string{"docker", "libvirt"}, Primary: "dev", SG: "/usr/bin/sg"}
	got := m.Wrap([]string{"/bin/bash", "-lc", "echo hi"})
	if len(got) != 4 || got[0] != "/usr/bin/sg" || got[1] != "docker" || got[2] != "-c" ||
		!strings.HasPrefix(got[3], "exec '/usr/bin/sg' 'libvirt' -c ") || !strings.Contains(got[3], "dev") {
		t.Fatalf("%q", got)
	}
	if got := (Missing{}).Wrap([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
}

// TestWrapRunsTheProgram runs the wrapped command with a stand-in sg that
// runs its -c through sh, as the real one does after setting the group:
// the program gets its arguments exactly.
func TestWrapRunsTheProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	dir := t.TempDir()
	sg := dir + "/sg"
	if err := writeExec(sg, "#!/bin/sh\necho \"sg $1\" >>\""+dir+"/log\"\n[ \"$2\" = -c ] || exit 9\nexec /bin/sh -c \"$3\"\n"); err != nil {
		t.Fatal(err)
	}
	m := Missing{Groups: []string{"docker", "libvirt"}, Primary: "dev", SG: sg}
	argv := m.Wrap([]string{"/bin/sh", "-c", `printf '%s|' "$@"`, "sh", "it's", "a $HOME", `"q"`})
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `it's|a $HOME|"q"|` {
		t.Fatalf("%q", out)
	}
	log, _ := readFile(dir + "/log")
	if strings.Join(strings.Fields(log), " ") != "sg docker sg libvirt sg dev" {
		t.Fatalf("sg ran as %q", log)
	}
}

func writeExec(p, s string) error { return os.WriteFile(p, []byte(s), 0o755) }
func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func TestNowUsesTheSourceAndForgets(t *testing.T) {
	n := 0
	s := fake([]string{"1000", "998"}, []int{1000})
	db := s.Database
	s.Database = func() (string, []string, error) { n++; return db() }
	old := Use(s)
	t.Cleanup(func() { Use(old) })
	if m := Now(); !reflect.DeepEqual(m.Groups, []string{"docker"}) {
		t.Fatalf("%+v", m)
	}
	Now()
	Forget()
	if got := Wrap([]string{"x"}); got[0] != "/usr/bin/sg" || got[1] != "docker" {
		t.Fatal(got)
	}
	if n != 2 {
		t.Fatalf("read the database %d times", n)
	}
}
