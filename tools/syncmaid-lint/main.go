package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syncmaid/tools/repolint"
)

func main() {
	root := flag.String("root", ".", "repository root directory")
	withVet := flag.Bool("vet", false, "also run go vet, go vet -tags e2e, and go tool mygo vet")
	flag.Parse()

	violations, err := repolint.Check(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "syncmaid-lint: %v\n", err)
		os.Exit(1)
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v.String())
	}
	if len(violations) > 0 {
		os.Exit(1)
	}

	if !*withVet {
		return
	}

	vetCommands := [][]string{
		{"vet", "./..."},
		{"vet", "-tags", "e2e", "./..."},
		{"tool", "mygo", "vet", "./..."},
	}
	for _, args := range vetCommands {
		cmd := exec.Command("go", args...)
		cmd.Dir = *root
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "syncmaid-lint: go %v failed: %v\n", args, err)
			os.Exit(1)
		}
	}
}
