package agentcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// profileMark marks the line Berth adds to a login profile.
const profileMark = "# Added by Berth: agent CLIs and tmux live in ~/.local/bin"

// EnsureLoginPATH makes ~/.local/bin part of the login shell's PATH, where
// the agents (and Berth's own tmux) go: loginPATH is that shell's PATH now
// and shell its path. Ubuntu's ~/.profile adds the folder once it exists,
// so most boxes need nothing; otherwise one marked line goes at the end of
// the profile the shell reads. It reports the file it changed, if any.
func EnsureLoginPATH(home, shell, loginPATH string) (string, error) {
	bin := filepath.Join(home, ".local", "bin")
	for _, d := range filepath.SplitList(loginPATH) {
		if filepath.Clean(d) == bin {
			return "", nil
		}
	}
	var file, line string
	switch filepath.Base(shell) {
	case "zsh":
		file, line = filepath.Join(home, ".zprofile"), `export PATH="$HOME/.local/bin:$PATH"`
	case "fish":
		file, line = filepath.Join(home, ".config", "fish", "conf.d", "berth-path.fish"), "fish_add_path $HOME/.local/bin"
	case "bash":
		// bash reads the first of these it finds, and only that one.
		file = filepath.Join(home, ".profile")
		for _, f := range []string{".bash_profile", ".bash_login"} {
			if _, err := os.Stat(filepath.Join(home, f)); err == nil {
				file = filepath.Join(home, f)
				break
			}
		}
		line = `export PATH="$HOME/.local/bin:$PATH"`
	default:
		file, line = filepath.Join(home, ".profile"), `export PATH="$HOME/.local/bin:$PATH"`
	}
	if b, err := os.ReadFile(file); err == nil && strings.Contains(string(b), profileMark) {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n%s\n%s\n", profileMark, line); err != nil {
		return "", err
	}
	return file, nil
}
