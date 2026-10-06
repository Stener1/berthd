// Command berth runs on a laptop: it pairs with boxes, runs the background
// agent, and manages forwards and URLs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/sean-brydon/berthd/internal/agent"
	"github.com/sean-brydon/berthd/internal/boxcmd"
	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/identity"
	"github.com/sean-brydon/berthd/internal/integrations"
	"github.com/sean-brydon/berthd/internal/pairing"
	"github.com/sean-brydon/berthd/internal/statefile"
	"github.com/sean-brydon/berthd/internal/trust"
	"github.com/sean-brydon/berthd/internal/version"
	"github.com/sean-brydon/berthd/internal/wire"
)

const usage = `berth — connect this laptop to development boxes

Boxes
  berth add ssh [user@]HOST [--name N] [--network NET] [--listen ADDR] [--address ADDR]
        [--identity FILE] [--trust-host-key SHA256:…] [--no-integrations] [-- SSH OPTIONS]
                                         Install berthd on a box over SSH (once), with hooks
                                         for the agent CLIs it has, and pair
  berth pair '<link>' [--name N] [--network NET]
                                         Pair with a box (link from berthd pair)
  berth invite [--boxes a,b] [--for NAME] [--yes] [--json]
                                         A join link for another computer of yours: one
                                         single-use code per box, valid ten minutes
  berth join '<link>'|- [--check] [--yes] [--json]
                                         Pair this computer with every box in a join link
  berth network login NAME               Join another tailnet (e.g. a personal one) to reach its boxes
  berth network proxy NAME HOST PORT     Connect stdin/stdout to HOST:PORT through it (SSH ProxyCommand)
  berth networks [--json]                List joined networks
  berth discover [--network NET] [--json]
                                         Machines on the tailnet that could be boxes
  berth boxes [--json]                   List paired boxes and whether they are online
  berth ping BOX                         Check a box answers and still trusts you
  berth upgrade BOX [--check] [--json]   Upgrade the box's daemon over berth (no SSH); --check only reports
  berth kit add|apply|list|save …        Set projects up the same way on every box; see berth kit help
  berth team show|setup|status|retry …   Set a box up the way your team's are, from <org>/.berth
                                         on GitHub (read with gh); see berth team help
  berth edit BOX/PROJECT[/WT] [FILE[:LINE[:COL]]] [--in EDITOR]
                                         Open a worktree, or a file at a line, in your editor
                                         (EDITOR: cursor, vscode, windsurf or zed; default: the first installed)
  berth ssh-config [--write] [--json]    Show (then write) the SSH hosts editors use: berth-<box>
  berth forget BOX                       Remove a box from this laptop

Reaching services
  berth url BOX PORT|SERVICE             Print the private URL for a service
  berth open BOX PORT|SERVICE            Open that URL in your browser
  berth forward BOX PORTS [--json]       Forward local ports: 3000, 8080:3000, 3000-3005
  berth forwards [--json]                List forwards
  berth unforward ID                     Stop and forget a forward
  berth route add '*.x.localhost' BOX PORT
                                         Send every matching host to a box port, Host unchanged
  berth routes [--json]                  List routes (berth route rm PATTERN removes one)

Sessions
  berth attach BOX/SESSION               Attach this terminal to an agent session (detach: Ctrl-b d)
  berth terminal BOX/SESSION             Open a new terminal window attached to a session
  berth emit TYPE [key=value...]         Announce an event on this laptop, e.g. agent.finished
  berth queue [--json]                   Prompts waiting for their box (berth queue rm|retry|send ID)

Agent
  berth status [--json]                  Boxes, forwards and the proxy at a glance
  berth doctor [BOX] [--json]            Check this computer (or a box) and how to fix it
  berth doctor --report                  A short, redacted report to paste into a chat
  berth events [BOX] [--json]            Stream events from this laptop and every box (or only BOX's)
  berth agent                            Run the agent in the foreground
  berth agent start                      Start the agent in the background, if it is not running
  berth agent restart [--if-stale]       Restart it once its work under way is done (--if-stale:
                                         only if it is older than this berth, as after an update)
  berth agent install|uninstall|status   Run the agent at login, restart it on crashes
  berth setup port80 [--remove]          Drop :1377 from URLs (asks for your admin password once)
  berth stop                             Stop the agent (and every forward)
  berth id                               Print this laptop's fingerprint
  berth version                          Print this build's version
  berth ui-token                         The desktop app's API address and token, as JSON

BERTH_HOME overrides the state directory.
`

func main() {
	// ssh runs SSH_ASKPASS with the prompt as its only argument.
	if os.Getenv(askpassMarker) == "1" && len(os.Args) == 2 {
		if err := askpass(os.Args[1:]); err != nil {
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "berth:", err)
		os.Exit(1)
	}
}

// helpText is what berth help prints: the laptop's commands, then the box
// commands (with the box in front), then the integrations.
func helpText() string {
	return usage + "\n" + boxcmd.Usage("berth", "BOX/") + "\n" + fmt.Sprintf(integrations.Usage, "berth")
}

type laptop struct {
	dir string
}

func (l laptop) identity() (*identity.Identity, error) {
	return identity.LoadOrCreate(filepath.Join(l.dir, "identity.pem"))
}
func (l laptop) boxes() *trust.Store { return trust.NewStore(filepath.Join(l.dir, "boxes.json")) }
func (l laptop) socket() string      { return filepath.Join(l.dir, "agent.sock") }

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(helpText())
		return nil
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Println(version.Line("berth"))
		return nil
	}
	home, err := statefile.Home()
	if err != nil {
		return err
	}
	l := laptop{dir: filepath.Join(home, "client")}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "pair":
		return pair(l, rest)
	case "invite":
		return invite(l, rest)
	case "join":
		return join(l, rest)
	case "setup":
		return setup(rest)
	case "doctor":
		return runDoctor(l, rest)
	case "route":
		return routeCommand(l, rest)
	case "discover":
		return discover(l, rest)
	case "routes":
		return listRoutes(l, rest)
	case "network":
		return networkCommand(l, rest)
	case "upgrade":
		return upgrade(l, rest)
	case "kit", "kits":
		return kitCommand(l, rest)
	case "team":
		return teamCommand(l, rest)
	case "ssh-config":
		return sshConfigCommand(l, rest)
	case "edit":
		return editCommand(l, rest)
	case "networks":
		return listNetworks(l, rest)
	case "add":
		if len(rest) == 0 || rest[0] != "ssh" {
			return errors.New(addSSHUsage)
		}
		return addSSH(l, rest[1:])
	case "boxes":
		return listBoxes(l, rest)
	case "ping":
		return ping(l, rest)
	case "forget":
		return forget(l, rest)
	case "url", "open":
		return serviceURL(l, cmd == "open", rest)
	case "forward":
		return addForward(l, rest)
	case "forwards":
		return listForwards(l, rest)
	case "unforward":
		return removeForward(l, rest)
	case "status":
		return status(l, rest)
	case "events":
		return streamEvents(l, rest)
	case "agent":
		return agentCommand(l, rest)
	case "stop":
		c := agent.NewClient(l.socket())
		if !c.Running(context.Background()) {
			fmt.Println("The berth agent is not running.")
			return nil
		}
		if err := c.Stop(context.Background()); err != nil {
			return err
		}
		fmt.Println("Stopped the berth agent.")
		return nil
	case "ui-token":
		// The desktop app's endpoint, for running its UI in a browser.
		if _, err := ensureAgent(l); err != nil {
			return err
		}
		tok, err := agent.UIToken(l.dir)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"url": fmt.Sprintf("http://127.0.0.1:%d", agent.DefaultUIPort), "token": tok,
		})
	case "id":
		id, err := l.identity()
		if err != nil {
			return err
		}
		fmt.Println(id.Fingerprint())
		return nil
	case "attach":
		return attach(l, rest)
	case "terminal":
		return openTerminal(rest)
	case "hook":
		integrations.Hook(rest, os.Stdin, os.Stdout, os.Stderr, func(e events.Event) error {
			c := agent.NewClient(l.socket())
			if !c.Running(context.Background()) {
				return nil
			}
			// A prompt's title names a box's session; the laptop has none.
			delete(e.Data, "title")
			e = integrations.StripAsk(e)
			return c.Call(context.Background(), "POST", "/v1/events", map[string]any{"type": e.Type, "origin": e.Origin, "data": e.Data}, nil)
		})
		return nil
	case "integrations":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return integrations.Install(rest, exe, os.Stdout)
	case "queue":
		return queueCmd(l, rest)
	case "emit":
		// An event for this laptop, unless it names a paired box first.
		if len(rest) > 0 {
			if _, ok, _ := l.boxes().ByName(rest[0]); ok {
				return runOnBox(l, args)
			}
		}
		return emitLocal(l, rest)
	}
	if _, ok := boxcmd.Commands[cmd]; ok {
		return runOnBox(l, args)
	}
	return fmt.Errorf("unknown command %q; run berth help", cmd)
}

// flags parses the common --json flag plus any command-specific ones. Flags
// may come before or after positional arguments.
func flags(name string, args []string, extra func(*flag.FlagSet)) (*flag.FlagSet, bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if extra != nil {
		extra(fs)
	}
	err := fs.Parse(flagsFirst(fs, args))
	return fs, *asJSON, err
}

// flagsFirst moves flags (with their values) ahead of positional arguments,
// because the flag package stops at the first positional one.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flagArgs = append(flagArgs, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !(ok && b.IsBoolFlag()) && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return append(append(flagArgs, "--"), positional...)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func pair(l laptop, args []string) error {
	var name, via *string
	fs, asJSON, err := flags("pair", args, func(fs *flag.FlagSet) {
		name = fs.String("name", "", "local name for the box (default: the name the box reports)")
		via = fs.String("network", "", "reach the box through this network (see berth networks)")
	})
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: berth pair '<link>' [--name N] [--network NET]")
	}
	if pairing.FindJoinLink(fs.Arg(0)) != "" {
		return errors.New("that is a join link for several boxes; use berth join")
	}
	tok, err := pairing.ParseToken(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := checkNetwork(*via); err != nil {
		return err
	}
	boxes := l.boxes()
	// Check the requested name before spending the single-use code on the box.
	if *name != "" {
		if err := checkName(*name); err != nil {
			return err
		}
		if existing, ok, err := boxes.ByName(*name); err != nil {
			return err
		} else if ok && existing.Fingerprint != tok.Fingerprint {
			return fmt.Errorf("a different box is already paired as %q; choose another --name", *name)
		}
	}
	id, err := l.identity()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	dial, err := networkDialer(l, *via)
	if err != nil {
		return err
	}
	reported, err := wire.PairVia(context.Background(), id, tok, trust.NameFromHostname(hostname, "laptop"), dial)
	if err != nil {
		return err
	}
	peer := trust.Peer{Address: tok.Address, Network: *via, Fingerprint: tok.Fingerprint, PairedAt: time.Now().UTC()}
	if *name != "" {
		peer.Name = *name
		err = boxes.Add(peer)
	} else {
		peer.Name = trust.NameFromHostname(reported, "box")
		peer.Name, err = boxes.AddWithFreeName(peer)
	}
	if err != nil {
		return fmt.Errorf("the box accepted this laptop, but saving it locally failed: %w; run berthd pair again", err)
	}
	// Let a running agent pick the box up now rather than at its next check.
	if c := agent.NewClient(l.socket()); c.Running(context.Background()) {
		c.Refresh(context.Background())
	}
	if asJSON {
		return printJSON(peer)
	}
	fmt.Printf("Paired with %s at %s (%s)\n", peer.Name, peer.Address, peer.Fingerprint.Short())
	return nil
}

func listBoxes(l laptop, args []string) error {
	_, asJSON, err := flags("boxes", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s.Boxes)
	}
	if len(s.Boxes) == 0 {
		fmt.Println("No paired boxes. On a box, run berthd pair, then berth pair '<link>' here.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tADDRESS\tLATENCY\tFINGERPRINT")
	for _, b := range s.Boxes {
		latency := "-"
		if b.LatencyMs > 0 {
			latency = strconv.FormatInt(b.LatencyMs, 10) + "ms"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.Name, b.State, b.Address, latency, b.Fingerprint[:12])
	}
	return w.Flush()
}

func ping(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: berth ping <box>")
	}
	client, err := l.boxClient(args[0])
	if err != nil {
		return err
	}
	defer client.Reset()
	box := client.Box()
	start := time.Now()
	if _, err := client.Ping(context.Background()); err != nil {
		return fmt.Errorf("%s: %w", box.Name, err)
	}
	fmt.Printf("%s: ok (%s)\n", box.Name, time.Since(start).Round(time.Millisecond))
	return nil
}

func forget(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: berth forget <box>")
	}
	p, err := l.boxes().Remove(args[0])
	if err != nil {
		return err
	}
	if c := agent.NewClient(l.socket()); c.Running(context.Background()) {
		c.Refresh(context.Background())
	}
	fmt.Printf("Forgot %s. On the box, berthd revoke removes this laptop's access too.\n", p.Name)
	return nil
}

func serviceURL(l laptop, open bool, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: berth url|open <box> <port|service>")
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if s.Proxy.Port == 0 {
		return fmt.Errorf("the local proxy is not running: %s", s.Proxy.Error)
	}
	url := serviceURLFor(args[1], args[0], s.Proxy.URLPort)
	fmt.Println(url)
	if !open {
		return nil
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, url).Run()
}

func addForward(l laptop, args []string) error {
	fs, asJSON, err := flags("forward", args, nil)
	if err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: berth forward <box> <ports>  (3000, 8080:3000, 3000-3005)")
	}
	maps, err := parsePorts(fs.Arg(1))
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	var added []agent.Forward
	for _, m := range maps {
		f, err := c.AddForward(context.Background(), fs.Arg(0), m.local, m.remote)
		if err != nil {
			return fmt.Errorf("localhost:%d: %w", m.local, err)
		}
		added = append(added, f)
		if !asJSON {
			fmt.Printf("%s  localhost:%d → %s:%d\n", f.ID, f.Local, f.Box, f.Remote)
		}
	}
	if asJSON {
		return printJSON(added)
	}
	return nil
}

func listForwards(l laptop, args []string) error {
	_, asJSON, err := flags("forwards", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s.Forwards)
	}
	if len(s.Forwards) == 0 {
		fmt.Println("No forwards. Add one with berth forward <box> <ports>.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLOCAL\tBOX\tREMOTE\tSTATE")
	for _, f := range s.Forwards {
		state := f.State
		if f.Error != "" {
			state += ": " + f.Error
		}
		fmt.Fprintf(w, "%s\tlocalhost:%d\t%s\t%d\t%s\n", f.ID, f.Local, f.Box, f.Remote, state)
	}
	return w.Flush()
}

func removeForward(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: berth unforward <id>")
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	f, err := c.RemoveForward(context.Background(), args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Stopped localhost:%d → %s:%d\n", f.Local, f.Box, f.Remote)
	return nil
}

func status(l laptop, args []string) error {
	_, asJSON, err := flags("status", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Refresh(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s)
	}
	if s.Proxy.Error != "" {
		fmt.Printf("proxy: %s\n", s.Proxy.Error)
	} else {
		fmt.Printf("proxy: %s\n", serviceURLFor("<port>", "<box>", s.Proxy.URLPort))
	}
	fmt.Println()
	if err := listBoxes(l, nil); err != nil {
		return err
	}
	if len(s.Forwards) > 0 {
		fmt.Println()
		return listForwards(l, nil)
	}
	return nil
}

// streamEvents prints the agent's events: this laptop's and every box's, or
// with a box name, only that box's.
func streamEvents(l laptop, args []string) error {
	fs, asJSON, err := flags("events", args, nil)
	if err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: berth events [BOX] [--json]")
	}
	boxName := fs.Arg(0)
	if boxName != "" {
		if _, ok, err := l.boxes().ByName(boxName); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("no paired box named %q; see berth boxes", boxName)
		}
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	return c.Events(ctx, eventPrinter(os.Stdout, boxName, asJSON))
}

// eventPrinter writes each event as a line, or as JSON; given a box name, it
// skips every event that is not that box's.
func eventPrinter(out io.Writer, boxName string, asJSON bool) func(agent.Event) {
	enc := json.NewEncoder(out)
	return func(e agent.Event) {
		if boxName != "" && e.Box != boxName {
			return
		}
		if asJSON {
			enc.Encode(e)
			return
		}
		line := boxcmd.Describe(e)
		if local, ok := e.Data["local"]; ok {
			line += fmt.Sprintf("  localhost:%v → %v", local, e.Data["remote"])
		}
		fmt.Fprintln(out, line)
	}
}

// emitLocal publishes an event on this laptop's agent, for tools running on
// the laptop (Cursor, a local Orca) to announce what they did.
func emitLocal(l laptop, args []string) error {
	if len(args) == 0 || !strings.Contains(args[0], ".") {
		return errors.New("usage: berth emit TYPE [key=value...]   (or berth emit BOX TYPE ... for a box)")
	}
	data := map[string]any{}
	origin := os.Getenv("BERTH_ORIGIN")
	for _, kv := range args[1:] {
		if o, ok := strings.CutPrefix(kv, "--origin="); ok {
			origin = o
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}
		data[k] = v
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	return c.Call(context.Background(), "POST", "/v1/events", map[string]any{"type": args[0], "origin": origin, "data": data}, nil)
}

// agentIfRunning returns a client for the agent when one is running, without
// starting one.
func agentIfRunning(l laptop) *agent.Client {
	c := agent.NewClient(l.socket())
	if c.Running(context.Background()) {
		return c
	}
	return nil
}
