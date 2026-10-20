// Command rules edits the filter rules in MongoDB (filter_rules) and keeps
// their history (filter_rules_log). Run it through make: rule-add,
// rule-remove, rules, rules-history.
//
//	rules add <tag|word> <allow|deny> <value>
//	rules remove <tag|word> <value>
//	rules list
//	rules history [n]
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
)

const usage = `usage:
  rules add <tag|word> <allow|deny> <value>
  rules remove <tag|word> <value>
  rules list
  rules history [n]`

// Store is MongoDB (implemented by *db.MongoDB).
type Store interface {
	FilterRules(ctx context.Context) (db.FilterRules, error)
	AddFilterRule(ctx context.Context, kind, action, value, by string) (db.FilterRule, error)
	RemoveFilterRule(ctx context.Context, kind, value, by string) (db.FilterRule, error)
	FilterRuleHistory(ctx context.Context, n int64) ([]db.RuleChange, error)
	TagInUse(ctx context.Context, tag string) (bool, error)
}

func main() {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		fmt.Fprintln(os.Stderr, "MONGO_URI is not set (load it with: set -a; . ./.env; set +a)")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m, err := db.NewMongoDB(ctx, mongoURI, "polymarket")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = m.Close(context.Background()) }()
	if err := m.EnsureFilterRules(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	out, err := run(ctx, m, os.Args[1:], os.Getenv("USER"))
	fmt.Print(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run executes one command and returns what to print.
func run(ctx context.Context, s Store, args []string, by string) (string, error) {
	var out strings.Builder
	if len(args) == 0 {
		return "", errors.New(usage)
	}
	cmd, args := args[0], args[1:]
	switch {
	case cmd == "add" && len(args) == 3:
		rule, err := s.AddFilterRule(ctx, args[0], args[1], args[2], by)
		if err != nil {
			return out.String(), err
		}
		fmt.Fprintf(&out, "added: %s %s %q\n", rule.Action, rule.Kind, rule.Value)
		if rule.Kind == db.KindTag {
			if used, err := s.TagInUse(ctx, rule.Value); err == nil && !used {
				fmt.Fprintf(&out, "WARNING: no market has the tag %q (tags are case-sensitive)\n", rule.Value)
			}
		}
	case cmd == "remove" && len(args) == 2:
		rule, err := s.RemoveFilterRule(ctx, args[0], args[1], by)
		if err != nil {
			return out.String(), err
		}
		fmt.Fprintf(&out, "removed: %s %s %q\n", rule.Action, rule.Kind, rule.Value)
	case cmd == "list" && len(args) == 0:
		// Shown after every edit anyway.
	case cmd == "history" && len(args) <= 1:
		return history(ctx, s, args)
	default:
		return "", errors.New(usage)
	}

	rules, err := s.FilterRules(ctx)
	if err != nil {
		return out.String(), err
	}
	printRules(&out, rules)
	if cmd != "list" {
		fmt.Fprintln(&out, "Applies to markets updated from now on; run make markets-awesome to recompute all.")
	}
	return out.String(), nil
}

// history lists the last n rule changes (20 by default).
func history(ctx context.Context, s Store, args []string) (string, error) {
	n := int64(20)
	if len(args) == 1 {
		v, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || v <= 0 {
			return "", fmt.Errorf("history: n must be a positive number, got %q", args[0])
		}
		n = v
	}
	changes, err := s.FilterRuleHistory(ctx, n)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&out, "%s  %-6s %-5s %-4s %q  %s\n", c.At.UTC().Format("2006-01-02 15:04"), c.Op, c.Action, c.Kind, c.Value, c.By)
	}
	if len(changes) == 0 {
		fmt.Fprintln(&out, "no changes yet")
	}
	return out.String(), nil
}

func printRules(out *strings.Builder, r db.FilterRules) {
	list := func(name string, values []string) {
		v := "(none)"
		if len(values) > 0 {
			v = strings.Join(values, ", ")
		}
		fmt.Fprintf(out, "%-12s %s\n", name+":", v)
	}
	list("allow tags", r.AllowTags)
	list("deny tags", r.DenyTags)
	list("allow words", r.AllowWords)
	list("deny words", r.DenyWords)
	if r.Empty() {
		fmt.Fprintln(out, "No deny rules: every market would be awesome, so market updates are paused.")
	}
}
