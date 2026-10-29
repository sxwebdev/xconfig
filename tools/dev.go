//go:build ignore

// Developer checks are run explicitly with go run tools/dev.go <command>.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
)

var modules = []string{
	".",
	"decoders/xconfigdotenv",
	"decoders/xconfigjson",
	"decoders/xconfigyaml",
	"sourcers/xconfigvault",
	"watchers/xconfigfsnotify",
	"tests/integration",
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := dispatch(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dispatch(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: go run tools/dev.go workspace|check")
	}
	if _, err := os.Stat("xconfig.go"); err != nil {
		return errors.New("run developer tools from the repository root")
	}
	switch args[0] {
	case "workspace":
		return workspace()
	case "check":
		return check(ctx)
	default:
		return fmt.Errorf("unknown developer command %q", args[0])
	}
}

// workspace writes the go.work that builds every module against the others in
// this checkout. Published manifests carry no replace directives, so before a
// release is tagged they only build within this workspace.
func workspace() error {
	var b strings.Builder
	b.WriteString("go 1.26.0\n\nuse (\n")
	for _, module := range modules {
		fmt.Fprintf(&b, "\t./%s\n", module)
	}
	b.WriteString(")\n")
	return os.WriteFile("go.work", []byte(b.String()), 0o644)
}

func check(ctx context.Context) error {
	if err := workspace(); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	// A module directory may hold a go.work of its own; use the generated one.
	env := []string{"GOWORK=" + filepath.Join(root, "go.work")}
	for _, module := range modules {
		if err := run(ctx, module, env, "go", "vet", "./..."); err != nil {
			return err
		}
		if err := run(ctx, module, env, "go", "test", "./...", "-race", "-count=1"); err != nil {
			return err
		}
	}
	manifest, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}
	if strings.Contains(string(manifest), "require") {
		return errors.New("core must have no external dependencies")
	}
	return nil
}

func run(ctx context.Context, dir string, env []string, args ...string) error {
	fmt.Printf("+ %s [%s]\n", strings.Join(args, " "), dir)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
