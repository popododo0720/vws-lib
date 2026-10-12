// Command consumer is a sample service that builds against vws-lib the way a real consumer does:
// as a separate module that requires it and reaches it through replace. CI builds it so a change
// that breaks consumers (module path, package layout, a removed or changed name) fails here first.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/popododo0720/vws-lib/errs"
	"github.com/popododo0720/vws-lib/obs"
)

func main() {
	ctx := context.Background()
	shutdown, err := obs.Setup(ctx, obs.ForService("vws-consumer-sample"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = shutdown(ctx) }()

	e := errs.New(errs.CodeNotFound, "sample instance not found", errs.WithKind(errs.KindInstance))
	fmt.Println(errs.CodeOf(e), errs.KindOf(e))
}
