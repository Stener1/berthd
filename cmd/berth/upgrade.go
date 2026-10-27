package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/version"
)

// upgrade replaces a box's daemon with the build shipped beside this berth,
// over berth's own connection: no SSH, and agent sessions keep running.
func upgrade(l laptop, args []string) error {
	var check, force bool
	fs, asJSON, err := flags("upgrade", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&check, "check", false, "only report whether the box runs an older daemon")
		fs.BoolVar(&force, "force", false, "put this berth's daemon on the box even when the box runs a newer release")
	})
	if err != nil || fs.NArg() != 1 {
		return errors.New("usage: berth upgrade BOX [--check [--json]] [--force]")
	}
	args = fs.Args()
	wc, err := l.boxClient(args[0])
	if err != nil {
		return err
	}
	defer wc.Reset()
	bc := box.NewClient(wc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	info, err := bc.Info(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w (a daemon too old to upgrade itself needs `berth add ssh` once)", args[0], err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := readDaemon(exe, "berthd-"+info.OS+"-"+info.Arch)
	if err != nil {
		return err
	}
	next := box.BuildID(binary)
	// A box on a newer release than this berth isn't outdated: this berth
	// is. Builds that can't be placed (dev, or a box too old to say its
	// release) are told apart by their bytes alone, as before.
	newer := boxIsNewer(info.Version, version.Version)
	if check {
		if asJSON {
			return printJSON(map[string]any{"box": args[0], "current": info.Build, "available": next, "outdated": next != info.Build && !newer, "newer": newer, "version": info.Version})
		}
		switch {
		case next == info.Build:
			fmt.Printf("%s runs this build (%s).\n", args[0], next)
		case newer:
			fmt.Printf("%s runs berthd %s, newer than this berth's %s: update this app, not the box.\n", args[0], info.Version, version.Version)
		default:
			fmt.Printf("%s runs %s; this berth ships %s. Upgrade with: berth upgrade %s\n", args[0], info.Build, next, args[0])
		}
		return nil
	}
	if next == info.Build {
		fmt.Printf("%s already runs this build (%s).\n", args[0], next)
		return nil
	}
	if newer && !force {
		return fmt.Errorf("%s runs berthd %s, newer than this berth's %s: update this app instead (--force puts the older daemon on it anyway)", args[0], info.Version, version.Version)
	}
	fmt.Printf("Uploading berthd-%s-%s to %s…\n", info.OS, info.Arch, args[0])
	if err := bc.Upgrade(ctx, binary); err != nil {
		return err
	}
	wc.Reset()
	for ctx.Err() == nil {
		time.Sleep(time.Second)
		if now, err := bc.Info(ctx); err == nil && now.Build == next {
			fmt.Printf("%s upgraded: %s → %s. Sessions kept running.\n", args[0], info.Build, next)
			return nil
		}
	}
	return fmt.Errorf("%s did not come back on the new build in time; check it with berth ping %s", args[0], args[0])
}

// boxIsNewer says whether a box's daemon is from a newer release than this
// berth's. Either one unplaced (a dev build, or a box too old to say its
// release) is not.
func boxIsNewer(box, mine string) bool {
	c, ok := version.Compare(box, mine)
	return ok && c > 0
}
