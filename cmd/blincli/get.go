package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/client"
)

// Registered here (not in the main.go literal) so this file is self-contained.
func init() {
	commands["get"] = (*app).cmdGet
	commands["devices"] = (*app).cmdDevices
}

func (a *app) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.stdout, string(b))
	return err
}

// cmdGet is read-only: it only issues GETs and never claims or creates a key.
func (a *app) cmdGet(args []string) int {
	fs, g := a.newFlags("get")
	var key, name string
	var slots int
	var all, asJSON bool
	str(fs, &key, "key", "k", "key to show (idx:N, led:N, row,col, or a claimed name)")
	str(fs, &name, "name", "n", "claim name to show")
	fs.IntVar(&slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&slots, "m", 0, "tab-slot count override")
	boolean(fs, &all, "all", "a", "list every registered key on the device")
	fs.BoolVar(&asJSON, "json", false, "print the daemon's response as JSON")
	if code, done := a.parse(fs, args); done {
		return code
	}
	if slots < 0 {
		return a.fail(fmt.Errorf("%w: -m must not be negative", client.ErrUsage))
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	cl, ep, fc, err := a.session(g)
	if err != nil {
		return a.fail(err)
	}
	if all {
		device, err := a.device(ctx, g, fc, cl)
		if err != nil {
			return a.fail(err)
		}
		keys, err := cl.ListKeys(ctx, device)
		if err != nil {
			return a.fail(err)
		}
		switch {
		case g.Quiet:
		case asJSON:
			return a.fail(a.printJSON(keys))
		default:
			renderKeyTable(a.stdout, keys)
		}
		return exitOK
	}
	plan, _, err := a.planKey(ctx, key, name)
	if err != nil {
		return a.fail(err)
	}
	if ep.Remote() { // same rule as set/clear (prepare)
		plan = plan.Qualify(a.host)
	}
	device, err := a.device(ctx, g, fc, cl)
	if err != nil {
		return a.fail(err)
	}
	ks, err := a.lookupKey(ctx, cl, device, plan, slots)
	if err != nil {
		return a.fail(err)
	}
	switch {
	case g.Quiet:
	case asJSON:
		return a.fail(a.printJSON(ks))
	default:
		renderKey(a.stdout, ks, time.Now())
	}
	return exitOK
}

// lookupKey reads plan's key; a named plan that isn't registered also tries
// its shared slot (where a write lands when the pool is empty or full).
func (a *app) lookupKey(ctx context.Context, cl *client.Client, device string, plan client.Plan, slots int) (client.KeyStatus, error) {
	switch plan.Mode {
	case client.ModeExplicit:
		return cl.GetKey(ctx, device, plan.Key)
	case client.ModeSlot:
		tabs, err := a.tabs(ctx, cl, device, slots)
		if err != nil {
			return client.KeyStatus{}, err
		}
		return cl.GetKey(ctx, device, client.SlotKey(plan.Tab, tabs))
	default:
		ks, err := cl.GetKey(ctx, device, plan.Name)
		if err == nil || !client.IsNotFound(err) {
			return ks, err
		}
		tabs, terr := a.tabs(ctx, cl, device, slots)
		if terr != nil {
			return client.KeyStatus{}, terr
		}
		return cl.GetKey(ctx, device, client.SharedKey(plan.Name, tabs))
	}
}

func (a *app) cmdDevices(args []string) int {
	fs, g := a.newFlags("devices")
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print the daemon's response as JSON")
	if code, done := a.parse(fs, args); done {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	cl, _, _, err := a.session(g)
	if err != nil {
		return a.fail(err)
	}
	if fs.NArg() == 0 {
		devs, err := cl.Devices(ctx)
		if err != nil {
			return a.fail(err)
		}
		switch {
		case g.Quiet:
		case asJSON:
			return a.fail(a.printJSON(devs))
		default:
			for _, d := range devs {
				state := "connected"
				if !d.Connected {
					state = "untethered"
				}
				_, _ = fmt.Fprintf(a.stdout, "%s  %s\n", d.Name, state)
			}
		}
		return exitOK
	}
	caps, err := cl.Capabilities(ctx, fs.Arg(0))
	if err != nil {
		return a.fail(err)
	}
	switch {
	case g.Quiet:
	case asJSON:
		return a.fail(a.printJSON(caps))
	default:
		renderCaps(a.stdout, fs.Arg(0), caps)
	}
	return exitOK
}

func humanDur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func renderKey(w io.Writer, k client.KeyStatus, now time.Time) {
	conn := "connected"
	if !k.Connected {
		conn = "untethered"
	}
	p := func(label, format string, args ...any) {
		_, _ = fmt.Fprintf(w, "%-9s %s\n", label, fmt.Sprintf(format, args...))
	}
	p("device", "%s  (%s)", k.Device, conn)
	loc := fmt.Sprintf("led:%d", k.LED)
	if k.Row != nil && k.Col != nil {
		loc += fmt.Sprintf(" (row %d, col %d)", *k.Row, *k.Col)
	}
	line := fmt.Sprintf("%s  -> %s  [%s]", k.Key, loc, k.Kind)
	if k.Source != nil && k.Source.Owner != "" {
		line += "  owner " + k.Source.Owner
	}
	p("key", "%s", line)
	if k.Claim != nil {
		left := "expired"
		if d := k.Claim.ExpiresAt.Sub(now); d > 0 {
			left = "in " + humanDur(d.Milliseconds())
		}
		p("claim", "expires %s  (%s)", k.Claim.ExpiresAt.Format(time.RFC3339), left)
	}
	if k.Source != nil {
		p("source", "%s %s", k.Source.Type, k.Source.Ref)
	}
	if k.Effect != nil {
		state := "finished"
		if k.Effect.Running {
			state = "running"
		}
		span := "loops"
		if k.Effect.DurationMS != nil {
			span = "of " + humanDur(*k.Effect.DurationMS)
		}
		p("effect", "%s  %s, %s elapsed, %s", k.Effect.Name, state, humanDur(k.Effect.ElapsedMS), span)
	}
	if k.Color != nil {
		p("color", "%s  (hsv %d,%d,%d)  desired value; keyboard RAM is not readable back", k.Color.Hex, k.Color.H, k.Color.S, k.Color.V)
	}
	if k.Source != nil {
		p("last set", "%s ago  (%s)", humanDur(k.Source.AgeMS), k.Source.SetAt.Format(time.RFC3339))
	}
}

func renderKeyTable(w io.Writer, keys []client.KeyStatus) {
	_, _ = fmt.Fprintf(w, "%-5s %-24s %-8s %-22s %s\n", "LED", "KEY", "KIND", "SOURCE", "AGE")
	for _, k := range keys {
		src, age := "-", "-"
		if k.Source != nil {
			src, age = k.Source.Type+" "+k.Source.Ref, humanDur(k.Source.AgeMS)
		}
		_, _ = fmt.Fprintf(w, "%-5d %-24s %-8s %-22s %s\n", k.LED, k.Key, k.Kind, src, age)
	}
}

func renderCaps(w io.Writer, name string, c client.Capabilities) {
	_, _ = fmt.Fprintf(w, "device    %s\nleds      %d\n", name, c.LEDCount)
	tabs := make([]string, len(c.Layout.Tabs))
	for i, t := range c.Layout.Tabs {
		tabs[i] = fmt.Sprint(t)
	}
	if len(tabs) == 0 {
		tabs = []string{"(none configured; blincli uses idx 0-5)"}
	}
	_, _ = fmt.Fprintf(w, "tabs      %s\n\nLED  ROW  COL\n", strings.Join(tabs, ","))
	for _, p := range c.Positions {
		row, col := fmt.Sprint(p.Row), fmt.Sprint(p.Col)
		if p.Row == 0xFF && p.Col == 0xFF {
			row, col = "-", "-"
		}
		_, _ = fmt.Fprintf(w, "%-4d %-4s %s\n", p.Index, row, col)
	}
}
