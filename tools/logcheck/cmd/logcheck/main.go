// Command logcheck runs the logcheck analyzer: go run ./tools/logcheck/cmd/logcheck ./...
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/radiobros/emailbridge/tools/logcheck"
)

func main() { singlechecker.Main(logcheck.Analyzer) }
