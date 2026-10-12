// Command consumer is a sample service that builds against vws-lib the way a real consumer does:
// as a separate module that requires it and reaches it through replace. CI builds it so a change
// that breaks consumers (module path, package layout, go directive) fails here first.
package main

import (
	"fmt"

	// The packages are imported for their existence. Once they export an API, call it here
	// (obs.Setup, errs.New) so a removed name also fails this build.
	_ "github.com/popododo0720/vws-lib/errs"
	_ "github.com/popododo0720/vws-lib/obs"
)

func main() {
	fmt.Println("vws-lib consumer sample")
}
