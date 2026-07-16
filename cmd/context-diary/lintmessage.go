package main

import (
	"fmt"
	"io"
	"os"

	"github.com/tae2089/context-diary/internal/trailer"
)

// cmdLintMessage lints a PR description or commit-message body from a file
// or stdin ("-"). A synthetic subject is prepended because the input is a
// body: GitHub's "PR title & description" squash setting composes the final
// commit message as title + blank line + body, so a trailers-only body is
// valid input here.
func cmdLintMessage(args []string) int {
	src := "-"
	if len(args) > 0 {
		src = args[0]
	}
	var (
		data []byte
		err  error
	)
	if src == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(src)
	}
	if err != nil {
		warnf("read message: %v", err)
		return 1
	}

	msg := "subject\n\n" + string(data)
	vs := trailer.Lint(msg)
	for _, v := range vs {
		fmt.Printf("%s: %s\n", v.Code, v.Msg)
	}
	if len(vs) > 0 {
		return 1
	}
	fmt.Println("message clean")
	return 0
}
