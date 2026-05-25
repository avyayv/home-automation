package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/avyayv/personal-automation-cli/cli/apple-home-cli/pkg/applehome"
)

func fatal(err any) {
	fmt.Fprintf(os.Stderr, "apple-home: %v\n", err)
	os.Exit(1)
}

func usage() {
	fmt.Print(`apple-home - inspect Apple Home and control supported devices

Usage:
  apple-home doctor [--json]
  apple-home list homes|rooms|devices|accessories|scenes [--json] [-v]
  apple-home find <query>
  apple-home myleviton devices [--json] [--raw] [--email E] [--password P]
  apple-home get <target> [--backend auto|myleviton|shortcuts]
  apple-home set <target> [on|off] [--brightness N] [--backend auto|myleviton|shortcuts]
  apple-home scene <name> [--backend shortcuts]
  apple-home shortcuts list|check|setup|payload
  apple-home shortcuts-template

Env:
  MYLEVITON_EMAIL, MYLEVITON_PASSWORD, APPLE_HOME_SHORTCUT
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "doctor":
		doctor(os.Args[2:])
	case "list":
		list(os.Args[2:])
	case "find":
		find(os.Args[2:])
	case "myleviton":
		myleviton(os.Args[2:])
	case "get":
		get(os.Args[2:])
	case "set":
		set(os.Args[2:])
	case "scene":
		scene(os.Args[2:])
	case "shortcuts":
		shortcuts(os.Args[2:])
	case "shortcuts-template":
		shortcutsTemplate()
	case "help", "--help", "-h":
		usage()
	default:
		fatal("unknown command " + os.Args[1])
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(b))
}

func table(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	printRow := func(cols []string) {
		for i, c := range cols {
			if i > 0 {
				fmt.Print("  ")
			}
			fmt.Print(c + strings.Repeat(" ", widths[i]-len(c)))
		}
		fmt.Println()
	}
	printRow(headers)
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = strings.Repeat("-", widths[i])
	}
	printRow(sep)
	for _, r := range rows {
		printRow(r)
	}
}

func list(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "json output")
	verbose := fs.Bool("v", false, "verbose")
	dbPath := fs.String("db", applehome.DefaultHomeDBPath(), "Home DB path")
	fs.BoolVar(verbose, "verbose", false, "verbose")
	_ = fs.Parse(reorderFlags(args, map[string]bool{"json": true, "v": true, "verbose": true}))
	if fs.NArg() != 1 {
		fatal("usage: apple-home list homes|rooms|devices|accessories|scenes")
	}
	db := applehome.NewHomeDB(*dbPath)
	switch fs.Arg(0) {
	case "homes":
		v, err := db.Homes()
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			printJSON(v)
			return
		}
		var rows [][]string
		for _, x := range v {
			rows = append(rows, []string{itoa(x.ID), x.Name, x.ModelID})
		}
		table([]string{"id", "name", "model_id"}, rows)
	case "rooms":
		v, err := db.Rooms()
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			printJSON(v)
			return
		}
		var rows [][]string
		for _, x := range v {
			rows = append(rows, []string{itoa(x.ID), x.Home, x.Name})
		}
		table([]string{"id", "home", "name"}, rows)
	case "devices", "accessories":
		v, err := db.Devices(*verbose)
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			printJSON(v)
			return
		}
		var rows [][]string
		for _, x := range v {
			r := []string{itoa(x.ID), x.Room, x.Name, x.Manufacturer, x.Model, x.Protocol}
			if *verbose {
				sort.Strings(x.Services)
				r = append(r, strings.Join(x.Services, ", "))
			}
			rows = append(rows, r)
		}
		head := []string{"id", "room", "name", "manufacturer", "model", "protocol"}
		if *verbose {
			head = append(head, "services")
		}
		table(head, rows)
	case "scenes":
		v, err := db.Scenes()
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			printJSON(v)
			return
		}
		var rows [][]string
		for _, x := range v {
			rows = append(rows, []string{itoa(x.ID), x.Home, x.Name, x.Type})
		}
		table([]string{"id", "home", "name", "type"}, rows)
	default:
		fatal("unknown list kind")
	}
}

func find(args []string) {
	if len(args) != 1 {
		fatal("usage: apple-home find <query>")
	}
	d, matches, err := applehome.NewHomeDB("").FindDevice(args[0])
	if err != nil {
		if len(matches) > 0 {
			deviceTable(matches)
		}
		fatal(err)
	}
	printJSON(d)
}

func myleviton(args []string) {
	if len(args) < 1 || args[0] != "devices" {
		fatal("usage: apple-home myleviton devices")
	}
	fs := flag.NewFlagSet("myleviton devices", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "json")
	raw := fs.Bool("raw", false, "raw")
	email := fs.String("email", "", "email")
	password := fs.String("password", "", "password")
	debug := fs.Bool("debug", false, "debug")
	_ = fs.Parse(reorderFlags(args[1:], map[string]bool{"json": true, "raw": true, "debug": true}))
	c := applehome.NewLevitonClient(*email, *password, *debug)
	devs, err := c.Devices()
	if err != nil {
		fatal(err)
	}
	if *raw || *asJSON {
		printJSON(devs)
		return
	}
	levitonTable(devs)
}

func get(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	backend := fs.String("backend", "auto", "backend")
	email := fs.String("email", "", "email")
	password := fs.String("password", "", "password")
	shortcut := fs.String("shortcut", "", "shortcut")
	debug := fs.Bool("debug", false, "debug")
	_ = fs.Parse(reorderFlags(args, map[string]bool{"debug": true}))
	if fs.NArg() != 1 {
		fatal("usage: apple-home get <target>")
	}
	if chooseBackend(*backend) == "shortcuts" {
		err := applehome.NewShortcutsBackend(*shortcut).Run(map[string]any{"action": "get", "target": fs.Arg(0)})
		if err != nil {
			fatal(err)
		}
		return
	}
	c := applehome.NewLevitonClient(*email, *password, *debug)
	v, err := c.GetState(fs.Arg(0))
	if err != nil {
		fatal(err)
	}
	printJSON(v)
}

func set(args []string) {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	backend := fs.String("backend", "auto", "backend")
	brightness := fs.Int("brightness", -1, "brightness 0..100")
	fs.IntVar(brightness, "b", -1, "brightness 0..100")
	email := fs.String("email", "", "email")
	password := fs.String("password", "", "password")
	shortcut := fs.String("shortcut", "", "shortcut")
	debug := fs.Bool("debug", false, "debug")
	_ = fs.Parse(reorderFlags(args, map[string]bool{"debug": true}))
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fatal("usage: apple-home set <target> [on|off] [--brightness N]")
	}
	var power *string
	if fs.NArg() == 2 {
		s := strings.ToLower(fs.Arg(1))
		if s != "on" && s != "off" {
			fatal("state must be on or off")
		}
		p := strings.ToUpper(s)
		power = &p
	}
	var bp *int
	if *brightness >= 0 {
		bp = brightness
	}
	if chooseBackend(*backend) == "shortcuts" {
		payload := map[string]any{"action": "set", "target": fs.Arg(0)}
		if power != nil {
			payload["power"] = *power
		}
		if bp != nil {
			payload["brightness"] = *bp
		}
		if err := applehome.NewShortcutsBackend(*shortcut).Run(payload); err != nil {
			fatal(err)
		}
		return
	}
	c := applehome.NewLevitonClient(*email, *password, *debug)
	v, err := c.SetState(fs.Arg(0), power, bp)
	if err != nil {
		fatal(err)
	}
	printJSON(v)
}

func scene(args []string) {
	fs := flag.NewFlagSet("scene", flag.ExitOnError)
	shortcut := fs.String("shortcut", "", "shortcut")
	backend := fs.String("backend", "shortcuts", "backend")
	_ = fs.Parse(reorderFlags(args, nil))
	if fs.NArg() != 1 {
		fatal("usage: apple-home scene <name>")
	}
	if *backend != "shortcuts" && *backend != "auto" {
		fatal("scene supports shortcuts backend")
	}
	if err := applehome.NewShortcutsBackend(*shortcut).Run(map[string]any{"action": "scene", "target": fs.Arg(0)}); err != nil {
		fatal(err)
	}
}

func doctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "json")
	_ = fs.Parse(reorderFlags(args, map[string]bool{"json": true}))
	type row struct {
		Check  string `json:"check"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}
	var rows []row
	db := applehome.NewHomeDB("")
	if _, err := os.Stat(applehome.DefaultHomeDBPath()); err == nil {
		rows = append(rows, row{"Home DB", "ok", applehome.DefaultHomeDBPath()})
	} else {
		rows = append(rows, row{"Home DB", "missing", applehome.DefaultHomeDBPath()})
	}
	h, _ := db.Homes()
	r, _ := db.Rooms()
	d, _ := db.Devices(false)
	s, _ := db.Scenes()
	rows = append(rows, row{"Home inventory", "ok", fmt.Sprintf(`{"homes":%d,"rooms":%d,"devices":%d,"scenes":%d}`, len(h), len(r), len(d), len(s))})
	cmd := exec.Command("/usr/bin/shortcuts", "list")
	out, err := cmd.Output()
	status := "ok"
	if err != nil {
		status = "failed"
	}
	rows = append(rows, row{"shortcuts CLI", status, fmt.Sprintf("%d shortcuts", countLines(string(out)))})
	lev := "not set"
	if os.Getenv("MYLEVITON_EMAIL") != "" && os.Getenv("MYLEVITON_PASSWORD") != "" {
		lev = "ok"
	}
	rows = append(rows, row{"My Leviton env", lev, "MYLEVITON_EMAIL/MYLEVITON_PASSWORD"})
	if *asJSON {
		printJSON(rows)
		return
	}
	var tr [][]string
	for _, x := range rows {
		tr = append(tr, []string{x.Check, x.Status, x.Detail})
	}
	table([]string{"check", "status", "detail"}, tr)
}

func shortcuts(args []string) {
	if len(args) == 0 {
		fatal("usage: apple-home shortcuts list|check|setup|payload")
	}
	switch args[0] {
	case "list":
		names, err := applehome.ListShortcuts()
		if err != nil {
			fatal(err)
		}
		for _, name := range names {
			fmt.Println(name)
		}
	case "check":
		fs := flag.NewFlagSet("shortcuts check", flag.ExitOnError)
		name := fs.String("name", applehome.DefaultShortcutName, "shortcut name")
		_ = fs.Parse(args[1:])
		ok, err := applehome.ShortcutExists(*name)
		if err != nil {
			fatal(err)
		}
		if ok {
			fmt.Printf("ok: shortcut %q exists\n", *name)
		} else {
			fmt.Printf("missing: shortcut %q\n", *name)
			os.Exit(1)
		}
	case "setup":
		shortcutsSetup()
	case "payload":
		shortcutPayload(args[1:])
	default:
		fatal("usage: apple-home shortcuts list|check|setup|payload")
	}
}

func shortcutPayload(args []string) {
	fs := flag.NewFlagSet("shortcuts payload", flag.ExitOnError)
	action := fs.String("action", "set", "set|get|scene")
	target := fs.String("target", "Kitchen Lights", "target accessory or scene")
	power := fs.String("power", "ON", "ON|OFF")
	brightness := fs.Int("brightness", -1, "brightness 0..100, omitted when negative")
	_ = fs.Parse(args)
	payload := map[string]any{"action": *action, "target": *target}
	if *action == "set" {
		payload["power"] = strings.ToUpper(*power)
		if *brightness >= 0 {
			payload["brightness"] = *brightness
		}
	}
	printJSON(payload)
}

func shortcutsSetup() {
	shortcutsTemplate()
	fmt.Println()
	fmt.Println("Current status:")
	ok, err := applehome.ShortcutExists(applehome.DefaultShortcutName)
	if err != nil {
		fmt.Println("  could not list shortcuts:", err)
	} else if ok {
		fmt.Printf("  ok: %q exists\n", applehome.DefaultShortcutName)
	} else {
		fmt.Printf("  missing: %q\n", applehome.DefaultShortcutName)
	}
	fmt.Println()
	fmt.Println("Fastest practical setup:")
	fmt.Println("  1. Open Shortcuts.app and create a shortcut named Apple Home CLI Bridge.")
	fmt.Println("  2. Add actions to read Shortcut Input as text/file, parse JSON into a dictionary, then branch on action.")
	fmt.Println("  3. In each branch, use Apple's Home actions. If Home's action picker won't accept variables for accessories on this macOS build, create explicit branches for your common targets/scenes.")
	fmt.Println("  4. Test with: apple-home set \"Kitchen Lights\" on --backend shortcuts")
	fmt.Println()
	fmt.Println("Example payload:")
	shortcutPayload([]string{"--action", "set", "--target", "Kitchen Lights", "--power", "ON", "--brightness", "50"})
}

func shortcutsTemplate() {
	fmt.Printf(`Generic Shortcuts bridge contract
--------------------------------
Create one shortcut named: %q

It should accept a file input containing JSON like:
  {"action":"set","target":"Kitchen Lights","power":"ON","brightness":50}
  {"action":"scene","target":"Good Night"}

The CLI will call:
  shortcuts run %q --input-path /tmp/payload.json

In Shortcuts, parse the file as JSON, branch on action, and use Home's native actions to control the chosen accessory/scene.
`, applehome.DefaultShortcutName, applehome.DefaultShortcutName)
}

func countLines(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			if strings.Contains(a, "=") || boolFlags[name] {
				continue
			}
			if i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func chooseBackend(b string) string {
	if b == "auto" || b == "" {
		return "myleviton"
	}
	return b
}
func itoa(i int) string { return strconv.Itoa(i) }

func deviceTable(devs []applehome.Device) {
	var rows [][]string
	for _, d := range devs {
		rows = append(rows, []string{itoa(d.ID), d.Room, d.Name, d.Manufacturer, d.Model})
	}
	table([]string{"id", "room", "name", "manufacturer", "model"}, rows)
}

func levitonTable(devs []applehome.LevitonDevice) {
	var rows [][]string
	for _, d := range devs {
		rows = append(rows, []string{d.ID, d.DisplayName(), d.DisplayModel(), d.Power, fmt.Sprint(d.Brightness)})
	}
	table([]string{"id", "name", "model", "power", "brightness"}, rows)
}
