package box

import (
	"context"
	"slices"
)

// Worktrees set up on first open: the git worktrees a repository already
// had when Team setup adopted it. Their per-worktree setup (the location's
// setup script, from its kit or its repository) runs the first time a
// terminal or an agent starts in each, not all at once during setup, and
// nothing touches their files before then.

// SetFirstOpen marks paths, worktrees of location, as set up on first open.
func (l *Locations) SetFirstOpen(location string, paths []string) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name != location {
				continue
			}
			for _, p := range paths {
				if !slices.Contains(all[i].FirstOpen, p) {
					all[i].FirstOpen = append(all[i].FirstOpen, p)
				}
			}
			return all, nil
		}
		return nil, ErrUnknownLocation
	})
}

// takeFirstOpen finds the location with dir waiting for its first open and
// takes it off the list, so its setup runs once.
func (l *Locations) takeFirstOpen(dir string) (location, path string) {
	all, _ := l.read()
	for _, s := range all {
		for _, p := range s.FirstOpen {
			if samePath(p, dir) {
				location, path = s.Name, p
			}
		}
	}
	if location == "" {
		return "", ""
	}
	taken := false
	l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name != location {
				continue
			}
			if at := slices.Index(all[i].FirstOpen, path); at >= 0 {
				all[i].FirstOpen = slices.Delete(all[i].FirstOpen, at, at+1)
				if len(all[i].FirstOpen) == 0 {
					all[i].FirstOpen = nil
				}
				taken = true
			}
		}
		return all, nil
	})
	if !taken {
		return "", ""
	}
	return location, path
}

// setUpOnFirstOpen runs the per-worktree setup of a worktree that waits for
// its first open, when a session starts in dir. Services that start with
// every worktree start once it has.
func (b *Box) setUpOnFirstOpen(from, dir string) {
	if b.Locations == nil {
		return
	}
	location, path := b.Locations.takeFirstOpen(dir)
	if location == "" {
		return
	}
	loc, err := b.Locations.Get(context.Background(), location)
	if err != nil {
		return
	}
	name := ""
	for _, w := range loc.Worktrees {
		if samePath(w.Path, path) {
			name = w.Name
		}
	}
	if name == "" {
		return
	}
	if loc.Scripts.Setup != "" {
		go b.lifecycle(from, "setup", loc, path, name, loc.Scripts.Setup, func() error {
			go b.startAutostart(loc.Name, name)
			return nil
		})
		return
	}
	go b.startAutostart(loc.Name, name)
}
