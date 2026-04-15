package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/userfacingerr"
)

func cmdErrors(_ context.Context, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return printErrorsHelp()
	}
	switch args[0] {
	case "list":
		return cmdErrorsList()
	default:
		return cmdErrorsLookup(args[0])
	}
}

func cmdErrorsList() error {
	fmt.Println("Elnath Error Catalog")
	for _, entry := range userfacingerr.All() {
		fmt.Printf("  %-10s  %s\n", entry.Code, entry.Title)
	}
	fmt.Println()
	fmt.Println("Run 'elnath errors <code>' for details.")
	return nil
}

func cmdErrorsLookup(raw string) error {
	code := userfacingerr.Code(strings.ToUpper(raw))
	if !strings.HasPrefix(string(code), "ELN-") {
		code = userfacingerr.Code("ELN-" + string(code))
	}
	entry, ok := userfacingerr.Lookup(code)
	if !ok {
		return fmt.Errorf("unknown error code %q - run 'elnath errors list'", raw)
	}
	fmt.Printf("\n%s - %s\n\n", entry.Code, entry.Title)
	fmt.Printf("What:  %s\n", entry.What)
	fmt.Printf("Why:   %s\n", entry.Why)
	fmt.Printf("Fix:   %s\n\n", entry.HowToFix)
	return nil
}

func printErrorsHelp() error {
	fmt.Print(`Usage: elnath errors <code|list>

Look up an Elnath error code for details and suggested fixes.

Commands:
  list         List all known error codes with short titles
  <code>       Show full details for the given code

Arguments:
  code         The ELN-XXX code shown in an error message.
               You may omit the "ELN-" prefix (e.g. "elnath errors 001").

Examples:
  elnath errors list
  elnath errors ELN-001
  elnath errors 030

See also: elnath setup --quickstart, elnath daemon start
`)
	return nil
}
