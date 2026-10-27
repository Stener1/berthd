package box

import (
	"fmt"
	"sync"
	"testing"
)

// A conversation's meta takes changes from several goroutines at once (a
// turn's time from the log, a mode from the app): each one keeps.
func TestChangesToAConversationsMetaAtOnceAllKeep(t *testing.T) {
	dir := t.TempDir()
	noteConversation(dir, func(c *Conversation) { c.Agent = "codex" })
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			noteConversation(dir, func(c *Conversation) { c.Title += fmt.Sprint(i%10, " ") })
		})
	}
	wg.Go(func() { noteConversation(dir, func(c *Conversation) { c.Mode = "bypassPermissions" }) })
	wg.Wait()
	c, _ := readConversation(dir)
	if c.Mode != "bypassPermissions" || len(c.Title) != 40 || c.Agent != "codex" {
		t.Fatalf("a change was lost: %+v", c)
	}
}
