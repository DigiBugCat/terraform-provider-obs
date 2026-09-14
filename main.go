package main

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/DigiBugCat/terraform-provider-obs/tfprovider"
)

func main() { os.Exit(run()) }

// run keeps the deferred profile flush ahead of the process exit code.
func run() int {
	// OBS_PROVIDER_CPUPROFILE=<path> writes a CPU profile of this plugin process for the
	// lifetime of one Terraform command. Diagnostic only; off unless set.
	if p := os.Getenv("OBS_PROVIDER_CPUPROFILE"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cpu profile:", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "cpu profile:", err)
			f.Close()
		} else {
			defer func() {
				pprof.StopCPUProfile()
				f.Close()
			}()
		}
	}
	if err := providerserver.Serve(context.Background(), tfprovider.New, providerserver.ServeOpts{
		Address: "registry.terraform.io/digibugcat/obs",
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
