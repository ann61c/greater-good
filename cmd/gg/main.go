package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const rules = "Rules: one active item at a time. When an item is over budget, park it now (commit WIP, stop its agents, " +
	"`gg park <id> <note>`) and start the top-ranked queued item. Only the user extends a budget. " +
	"\"Almost done\" is not a reason to continue. Parking is reversible, so do not wait for the user."

const usage = `[greater-good] Priority ledger for this project. You maintain it yourself; the user never runs gg.
When the user gives you work that will take over 30 minutes, runs in background agents, or is several tasks, record each as an item before starting it:
  gg add <id> "<title>" --why "<what the user gains>" --budget <2h|90m>
  gg rank <id> <id> ...     order by importance, taken from the user's own words; how close an item is to done does not count
  gg start <id>             one active item at a time
  gg park <id> "<note>"     gg done <id>     gg status     gg log
Budget is the most time the item is worth to the user. When the user states or changes priorities, run ` + "`gg rank`" + ` right away.
After adding or re-ranking, tell the user the order and budgets in one line. Only the user extends a budget (then run ` + "`gg budget <id> <dur>`" + `).`

const help = `gg: a priority ledger and a budget clock for coding agents.

Usage:
  gg add <id> "<title>" --why "<text>" --budget <dur>
                             add an item at the bottom of the ranking
  gg rank <id> <id> ...      move these items to the top, in this order
  gg start <id>              start the clock; one active item at a time
  gg park <id> "<note>"      stop the clock, keep a hand-over note
  gg done <id>               close an item
  gg budget <id> <dur>       set an item's budget
  gg status                  what the agent sees
  gg line [dir] [--session <id>]
                             the status line row
  gg log                     history: what was started, parked, re-ranked, and when
  gg hook                    Claude Code hook; reads the event JSON on stdin
  gg help                    this text
  gg man                     the man page, as roff

Durations look like 2h, 90m or 1h30m.
Ledgers are JSON files in ~/.greater-good/, one per git repository. GG_HOME overrides the directory.
Full manual: man gg
`

//go:embed gg.1
var manPage string

const layout = "2006-01-02T15:04:05"

type Item struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Why       string  `json:"why"`
	BudgetS   int     `json:"budget_s"`
	Status    string  `json:"status"`
	SpentS    float64 `json:"spent_s"`
	StartedAt *string `json:"started_at"`
	Note      string  `json:"note"`
}

type Event struct {
	At     string `json:"at"`
	Event  string `json:"event"`
	ID     string `json:"id"`
	Detail string `json:"detail"`
}

type Ledger struct {
	Items  []*Item `json:"items"`
	Events []Event `json:"events"`
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

func now() time.Time {
	if s, ok := os.LookupEnv("GG_NOW"); ok {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err != nil {
			die("bad GG_NOW: %v", err)
		}
		return t
	}
	return time.Now()
}

var durRe = regexp.MustCompile(`(\d+)([hm])`)

func dur(s string) int {
	t := 0
	for _, m := range durRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		if m[2] == "h" {
			n *= 60
		}
		t += n * 60
	}
	return t
}

func fmtDur(s float64) string {
	h, m := int(s)/60/60, int(s)/60%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}

func root(dir string) string {
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	for d := dir; ; d = filepath.Dir(d) {
		g := filepath.Join(d, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				return d
			}
			b, _ := os.ReadFile(g)
			p := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
			if !filepath.IsAbs(p) {
				p = filepath.Join(d, p)
			}
			return filepath.Dir(filepath.Dir(filepath.Dir(p)))
		}
		if d == "/" {
			return dir
		}
	}
}

func path(dir string) string {
	home := os.Getenv("GG_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".greater-good")
	}
	return filepath.Join(home, strings.ReplaceAll(root(dir), "/", "-")+".json")
}

func load(p string) *Ledger {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	l := &Ledger{}
	if err := json.Unmarshal(b, l); err != nil {
		die("%v", err)
	}
	return l
}

// ledger finds the ledger for dir. A session remembers the last ledger it saw,
// so it still finds it when dir leaves the repository.
func ledger(dir, session string) (string, *Ledger) {
	p := path(dir)
	l := load(p)
	if session == "" {
		return p, l
	}
	b := filepath.Join(filepath.Dir(p), "sessions", filepath.Base(session))
	if l != nil {
		os.MkdirAll(filepath.Dir(b), 0o755)
		os.WriteFile(b, []byte(p), 0o644)
	} else if bp, err := os.ReadFile(b); err == nil {
		p = string(bp)
		l = load(p)
	}
	return p, l
}

func save(p string, l *Ledger) {
	b, _ := json.MarshalIndent(l, "", "  ")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		die("%v", err)
	}
}

func elapsed(i *Item) float64 {
	if i.Status != "active" {
		return i.SpentS
	}
	t, _ := time.ParseInLocation(layout, *i.StartedAt, time.Local)
	return i.SpentS + now().Sub(t).Seconds()
}

func over(i *Item) bool { return i.Status == "active" && elapsed(i) > float64(i.BudgetS) }

func live(l *Ledger) (r []*Item) {
	for _, i := range l.Items {
		if i.Status != "done" {
			r = append(r, i)
		}
	}
	return
}

func status(l *Ledger) string {
	items := live(l)
	if len(items) == 0 {
		return ""
	}
	var act *Item
	for _, i := range items {
		if i.Status == "active" {
			act = i
			break
		}
	}
	out := []string{"[greater-good] " + now().Format("2006-01-02 15:04")}
	actIdx := -1
	for n, i := range items {
		spent, b := fmtDur(elapsed(i)), fmtDur(float64(i.BudgetS))
		rank := fmt.Sprintf("rank %d", n+1)
		if i == act {
			actIdx = n
			rank += fmt.Sprintf(" of %d", len(items))
		}
		head := fmt.Sprintf("%s #%s (%s) %s:", strings.ToUpper(i.Status), i.ID, rank, i.Title)
		switch i.Status {
		case "active":
			s := fmt.Sprintf("%s %s spent of %s budget.", head, spent, b)
			if over(i) {
				s += fmt.Sprintf(" OVER BUDGET by %s.", fmtDur(elapsed(i)-float64(i.BudgetS)))
			}
			out = append(out, s, "  why: "+i.Why)
		case "parked":
			out = append(out, fmt.Sprintf("%s %s spent of %s budget. note: %s", head, spent, b, i.Note))
		default:
			out = append(out, fmt.Sprintf("%s budget %s, %s spent. why: %s", head, b, spent, i.Why))
		}
	}
	if act != nil {
		for _, i := range items[:actIdx] {
			if i.Status == "queued" && i.SpentS == 0 {
				out = append(out, fmt.Sprintf("WARNING: #%s outranks the active item and has not started.", i.ID))
				break
			}
		}
	} else {
		out = append(out, fmt.Sprintf("Nothing active. Next by rank: #%s.", items[0].ID))
	}
	return strings.Join(append(out, rules), "\n")
}

func line(l *Ledger) string {
	var out []string
	for n, i := range live(l) {
		t := []rune(i.Title)
		if len(t) > 20 {
			t = t[:20]
		}
		s := fmt.Sprintf("#%s %s", i.ID, string(t))
		switch i.Status {
		case "active":
			c, o := "1", ""
			if over(i) {
				c, o = "1;31", " OVER"
			}
			s = fmt.Sprintf("\033[%sm%d▶ %s %s/%s%s\033[0m", c, n+1, s, fmtDur(elapsed(i)), fmtDur(float64(i.BudgetS)), o)
		case "parked":
			s = fmt.Sprintf("\033[2m%d %s (parked)\033[0m", n+1, s)
		default:
			s = fmt.Sprintf("%d %s", n+1, s)
		}
		out = append(out, s)
	}
	return strings.Join(out, " · ")
}

func hook() {
	var d struct {
		Event  string `json:"hook_event_name"`
		Cwd    string `json:"cwd"`
		Sess   string `json:"session_id"`
		Active bool   `json:"stop_hook_active"`
	}
	json.NewDecoder(os.Stdin).Decode(&d)
	_, l := ledger(d.Cwd, d.Sess)
	switch {
	case d.Event == "SessionStart":
		fmt.Println(usage)
		if l != nil && len(live(l)) > 0 {
			fmt.Printf("\n%s\n", status(l))
		}
	case l == nil:
	case d.Event == "UserPromptSubmit":
		if len(live(l)) > 0 {
			fmt.Println(status(l))
		}
	case d.Event == "Stop" && !d.Active:
		for _, a := range l.Items {
			if !over(a) {
				continue
			}
			var next *Item
			for _, i := range l.Items {
				if i.Status == "queued" {
					next = i
					break
				}
			}
			goNext := "`gg start` the top-ranked queued item"
			if next != nil {
				goNext = fmt.Sprintf("`gg start %s` (top-ranked queued: %s)", next.ID, next.Title)
			}
			out, _ := json.Marshal(map[string]string{"decision": "block", "reason": fmt.Sprintf(
				"#%s %s is over budget: %s spent of %s. Commit WIP, `gg park %s <note>`, then %s. "+
					"If the user explicitly extended the budget, run `gg budget %s <dur>` instead.",
				a.ID, a.Title, fmtDur(elapsed(a)), fmtDur(float64(a.BudgetS)), a.ID, goNext, a.ID)})
			fmt.Println(string(out))
			break
		}
	}
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"help"}
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(help)
		return
	case "man":
		fmt.Print(manPage)
		return
	case "hook":
		hook()
		return
	}
	dir, _ := os.Getwd()
	session := ""
	if cmd == "line" {
		for i := 0; i < len(args); i++ {
			if args[i] == "--session" && i+1 < len(args) {
				i++
				session = args[i]
			} else {
				dir = args[i]
			}
		}
	}
	p, l := ledger(dir, session)
	switch cmd {
	case "status", "line":
		if l != nil && len(live(l)) > 0 {
			fmt.Println(map[string]func(*Ledger) string{"status": status, "line": line}[cmd](l))
		}
		return
	case "log":
		if l != nil {
			var s []string
			for _, e := range l.Events {
				s = append(s, fmt.Sprintf("%s %s #%s %s", e.At, e.Event, e.ID, e.Detail))
			}
			fmt.Println(strings.Join(s, "\n"))
		}
		return
	}
	if l == nil {
		l = &Ledger{Items: []*Item{}, Events: []Event{}}
	}
	get := func(id string) *Item {
		for _, i := range l.Items {
			if i.ID == id {
				return i
			}
		}
		die("no item %s", id)
		return nil
	}
	var id, detail string
	switch cmd {
	case "add":
		var pos []string
		var why, budget string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--why":
				i++
				why = args[i]
			case "--budget":
				i++
				budget = args[i]
			default:
				pos = append(pos, args[i])
			}
		}
		if len(pos) != 2 || why == "" || budget == "" {
			die("usage: gg add <id> <title> --why <text> --budget <dur>")
		}
		id, detail = pos[0], why
		l.Items = append(l.Items, &Item{ID: id, Title: pos[1], Why: why, BudgetS: dur(budget), Status: "queued"})
	case "rank":
		var top []*Item
		seen := map[*Item]bool{}
		for _, a := range args {
			i := get(a)
			top = append(top, i)
			seen[i] = true
		}
		for _, i := range l.Items {
			if !seen[i] {
				top = append(top, i)
			}
		}
		l.Items, detail = top, strings.Join(args, " ")
	case "budget":
		id, detail = args[0], args[1]
		get(id).BudgetS = dur(detail)
	case "start", "park", "done":
		id = args[0]
		it := get(id)
		if cmd == "start" {
			for _, c := range l.Items {
				if c.Status == "active" {
					die("#%s is active; park or finish it first", c.ID)
				}
			}
			s := now().Format(layout)
			it.Status, it.StartedAt = "active", &s
			break
		}
		if it.Status == "active" {
			it.SpentS, it.StartedAt = elapsed(it), nil
		}
		it.Status = map[string]string{"park": "parked", "done": "done"}[cmd]
		if cmd == "park" {
			it.Note, detail = args[1], args[1]
		}
	default:
		die("unknown command %s", cmd)
	}
	l.Events = append(l.Events, Event{now().Format(layout), cmd, id, detail})
	save(p, l)
}
