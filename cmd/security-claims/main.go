package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/quality/securityclaims"
)

func main() {
	check := flag.Bool("check", false, "fail if the committed security claims differ from the registry")
	output := flag.String("output", "", "optional single document path to update or check")
	flag.Parse()

	var err error
	if *check {
		if *output == "" {
			err = securityclaims.CheckAllDocuments()
		} else {
			err = securityclaims.CheckDocument(*output)
		}
	} else if *output == "" {
		err = securityclaims.UpdateAllDocuments()
	} else {
		err = securityclaims.UpdateDocument(*output)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
