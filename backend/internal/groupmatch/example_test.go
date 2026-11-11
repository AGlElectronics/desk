// Code generated from con-services shared/go/groupmatch; DO NOT EDIT. Run scripts/sync-shared.

package groupmatch_test

import (
	"fmt"

	"github.com/AGlElectronics/desk/backend/internal/groupmatch"
)

func Example() {
	full := groupmatch.Config{}
	last := groupmatch.Config{Mode: groupmatch.ModeLast}
	token := []string{"/departments/security"}

	fmt.Println(full.Match(token, "departments/security"), full.Match(token, "security"))
	fmt.Println(last.Match(token, "security"))
	fmt.Printf("%+v\n", groupmatch.NewGrant(" /Departments/Security "))
	// Output:
	// true false
	// true
	// {Stored:departments/security Display:/Departments/Security}
}
