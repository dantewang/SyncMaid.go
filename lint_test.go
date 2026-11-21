package main

import (
	"syncmaid/tools/repolint"
	"testing"
)

func TestRepoLint(t *testing.T) {
	violations, err := repolint.Check(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v.String())
	}
}
