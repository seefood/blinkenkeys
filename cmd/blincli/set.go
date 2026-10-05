package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/seefood/blinkenkeys/internal/client"
)

// Registered from init so main.go's dispatch table needs no edit.
func init() {
	commands["set"] = (*app).cmdSet
	commands["clear"] = (*app).cmdClear
}

const defaultSlots = 6

// keyFlags are the options shared by set and clear.
type keyFlags struct {
	key, name  string
	slots      int
	ifDetected bool
}

func addKeyFlags(fs *flag.FlagSet, k *keyFlags) {
	str(fs, &k.key, "key", "k", "key position: name, R,C, led:N or idx:N (default: derived from the terminal)")
	str(fs, &k.name, "name", "n", "register under this name (claims a key from the pool)")
	fs.IntVar(&k.slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&k.slots, "m", 0, "tab-slot count override")
	fs.BoolVar(&k.ifDetected, "if-detected", false, "exit 0 silently if no key can be derived")
}

// tabs returns the tab-slot keys: 0..n-1 when n > 0, else the daemon's
// layout.tabs, else 0..5. It never returns an empty slice.
func (a *app) tabs(ctx context.Context, cl *client.Client, device string, n int) ([]uint16, error) {
	if n > 0 {
		return client.DefaultTabs(n), nil
	}
	caps, err := cl.Capabilities(ctx, device)
	if err != nil {
		return nil, err
	}
	if len(caps.Layout.Tabs) > 0 {
		return caps.Layout.Tabs, nil
	}
	return client.DefaultTabs(defaultSlots), nil
}

// prepared is everything a key-addressing command needs after planning.
type prepared struct {
	cl     *client.Client
	device string
	plan   client.Plan
	slots  int // <= 0: ask the daemon
	g      *globals
}

// prepare plans the key (no network), then resolves endpoint and device.
// skip is true when --if-detected applies and no key could be derived.
func (a *app) prepare(ctx context.Context, g *globals, k keyFlags) (p prepared, skip bool, err error) {
	plan, _, err := a.planKey(ctx, k.key, k.name)
	if err != nil {
		if errors.Is(err, client.ErrNoKey) && k.ifDetected {
			return p, true, nil
		}
		return p, false, err
	}
	cl, ep, fc, err := a.session(g)
	if err != nil {
		return p, false, err
	}
	if ep.Remote() {
		plan = plan.Qualify(a.host)
	}
	device, err := a.device(ctx, g, fc, cl)
	if err != nil {
		return p, false, err
	}
	slots := k.slots
	if slots <= 0 {
		slots = fc.Slots // tabs() treats <= 0 as "ask the daemon"
	}
	a.vlog(g, "device %s, key mode %d, owner %q", device, plan.Mode, plan.Owner)
	return prepared{cl: cl, device: device, plan: plan, slots: slots, g: g}, false, nil
}

func (a *app) cmdSet(args []string) int {
	fs, g := a.newFlags("set")
	var k keyFlags
	var col, eff, state string
	addKeyFlags(fs, &k)
	str(fs, &col, "color", "c", "color: #rrggbb, H,S,V (0-255) or a CSS/X11 name")
	str(fs, &eff, "effect", "e", "effect name")
	str(fs, &state, "state", "s", "template state, program/state (e.g. claude/idle)")
	if code, done := a.parse(fs, args); done {
		return code
	}
	n := 0
	for _, v := range []string{col, eff, state} {
		if v != "" {
			n++
		}
	}
	if n != 1 || fs.NArg() != 0 {
		return a.fail(fmt.Errorf("%w: set needs exactly one of -c/--color, -e/--effect, -s/--state", client.ErrUsage))
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	p, skip, err := a.prepare(ctx, g, k)
	if err != nil {
		return a.fail(err)
	}
	if skip {
		return exitOK
	}
	body := client.PutBody{Color: col, Effect: eff, State: state, Owner: p.plan.Owner}
	return a.fail(a.doSet(ctx, p, body))
}

// doSet performs the write for p's plan.
func (a *app) doSet(ctx context.Context, p prepared, body client.PutBody) error {
	switch p.plan.Mode {
	case client.ModeExplicit:
		body.Owner = ""
		return p.cl.Put(ctx, p.device, p.plan.Key, body)
	case client.ModeSlot:
		tabs, err := a.tabs(ctx, p.cl, p.device, p.slots)
		if err != nil {
			return err
		}
		return p.cl.Put(ctx, p.device, client.SlotKey(p.plan.Tab, tabs), body)
	default:
		err := p.cl.Put(ctx, p.device, p.plan.Name, body)
		if !client.IsConflict(err) {
			return err
		}
		tabs, terr := a.tabs(ctx, p.cl, p.device, p.slots)
		if terr != nil {
			return terr
		}
		shared := client.SharedKey(p.plan.Name, tabs)
		a.vlog(p.g, "pool full or empty; sharing slot %s", shared)
		return p.cl.Put(ctx, p.device, shared, body)
	}
}

func (a *app) cmdClear(args []string) int {
	fs, g := a.newFlags("clear")
	var k keyFlags
	var force bool
	addKeyFlags(fs, &k)
	boolean(fs, &force, "force", "f", "clear even if another session owns the key")
	if code, done := a.parse(fs, args); done {
		return code
	}
	if fs.NArg() != 0 {
		return a.fail(fmt.Errorf("%w: clear takes no arguments", client.ErrUsage))
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	p, skip, err := a.prepare(ctx, g, k)
	if err != nil {
		return a.fail(err)
	}
	if skip {
		return exitOK
	}
	owner := p.plan.Owner
	if force {
		owner = ""
	}
	return a.fail(a.doClear(ctx, p, owner))
}

// doClear removes the key; a key that isn't there (404 everywhere) is success.
// Slot and shared-slot clears are conditional on owner so one tab never
// blanks a key another session now holds.
func (a *app) doClear(ctx context.Context, p prepared, owner string) error {
	ignore404 := func(err error) error {
		if client.IsNotFound(err) {
			return nil
		}
		return err
	}
	switch p.plan.Mode {
	case client.ModeExplicit:
		return ignore404(p.cl.Delete(ctx, p.device, p.plan.Key, ""))
	case client.ModeSlot:
		tabs, err := a.tabs(ctx, p.cl, p.device, p.slots)
		if err != nil {
			return err
		}
		return ignore404(p.cl.Delete(ctx, p.device, client.SlotKey(p.plan.Tab, tabs), owner))
	default:
		// The claim by name belongs to this identity by construction.
		err := p.cl.Delete(ctx, p.device, p.plan.Name, "")
		if err == nil || !client.IsNotFound(err) {
			return err
		}
		tabs, terr := a.tabs(ctx, p.cl, p.device, p.slots)
		if terr != nil {
			return terr
		}
		return ignore404(p.cl.Delete(ctx, p.device, client.SharedKey(p.plan.Name, tabs), owner))
	}
}
